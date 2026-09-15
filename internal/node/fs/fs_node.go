package fs

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

const (
	crusoeCloudDNSNFSHost = "nfs.crusoecloudcompute.com"

	// dnsFallbackLocation is the single region where the legacy secondary-cluster
	// fallback routes mounts through DNS (remoteports=dns) instead of configured
	// IPs (see useDNSForMount). It is a deliberate, region-scoped carveout; the
	// value is a public region slug. If the carveout needs to outlive this
	// region, drive it off a CLI flag rather than hardcoding.
	dnsFallbackLocation = "eu-iceland1-a"

	// dnsRemotePorts aliases crusoe.DNSRemotePortsValue (the single source of
	// truth for the vastnfs "remoteports=dns" sentinel) so the two packages
	// cannot silently diverge. The value "dns" tells the NFS kernel module to
	// resolve the server name itself via the dns_resolver keyring upcall, rather
	// than being handed an explicit IP list.
	dnsRemotePorts = crusoe.DNSRemotePortsValue
)

type Node struct {
	csi.UnimplementedNodeServer
	CrusoeClient     *crusoeapi.APIClient
	CrusoeHTTPClient *http.Client
	HostInstance     *crusoeapi.InstanceV1Alpha5
	Mounter          *mount.SafeFormatAndMount
	Resizer          *mount.ResizeFs

	// VolumeLocks serialises node operations per target path. Zero value is
	// usable, so it needs no explicit initialisation. See node.VolumeLocks.
	// Placed with the pointer-bearing fields: govet fieldalignment wants them
	// contiguous, and sync.Map inside it carries pointers.
	VolumeLocks node.VolumeLocks

	CrusoeAPIEndpoint string
	NFSHost           string
	DiskType          common.DiskType
	PluginName        string
	PluginVersion     string
	NFSRemotePorts    string
	Capabilities      []*csi.NodeServiceCapability
	MaxVolumesPerNode int64
}

// NodeStageVolume mounts the export once per node at the staging target path. The
// CO (kubelet) calls this once per volume per node before any NodePublishVolume,
// and reference-counts it, so pod restarts bind/unbind (NodePublish/NodeUnpublish)
// against a mount that persists rather than re-mounting the export each time.
func (d *Node) NodeStageVolume(ctx context.Context, request *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse,
	error,
) {
	klog.Infof("Received request to stage volume: %+v", request)

	stagingTargetPath := request.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%s: NodeStageVolume", node.ErrVolumePathEmpty)
	}

	// Serialise on the staging target path. kubelet re-drives NodeStageVolume when
	// its deadline expires, and two pods first landing on a node both drive the
	// device mount, so two concurrent mount(2) calls for one staging path can both
	// pass the already-mounted check and collide with EBUSY. This is the same race
	// CRUSOE-97438 closed for NodePublishVolume, moved to the staging key.
	if !d.VolumeLocks.TryAcquire(stagingTargetPath) {
		klog.Warningf("operation already in progress for staging target path %s, returning Aborted", stagingTargetPath)

		return nil, status.Errorf(codes.Aborted, node.VolumeOperationAlreadyExistsFmt, stagingTargetPath)
	}
	defer d.VolumeLocks.Release(stagingTargetPath)

	err := d.ensureStaged(ctx, stagingTargetPath, request.GetVolumeId(),
		request.GetVolumeCapability(), request.GetVolumeContext())
	if err != nil {
		klog.Errorf("failed to stage volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to stage volume %s: %s", request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully staged volume: %s", request.GetVolumeId())

	return &csi.NodeStageVolumeResponse{}, nil
}

// ensureStaged mounts the export at the staging path if it is not already mounted
// there, resolving the NFS target and NFS-vs-virtiofs flag first. It is the shared
// core of NodeStageVolume and the stage step of NodePublishVolume, so both reach a
// staged export by the same path (CRUSOE-103809). Callers must already hold the
// staging-path lock.
//
// It short-circuits on an already-mounted staging path with a cheap IsMountPoint
// check before any Crusoe API call. That keeps the common publish (kubelet staged
// first, or an earlier publish staged) free of API calls, so a Crusoe API outage
// cannot stop a pod that only needs a bind from a mount that already exists. The
// full path (flag fetch, target resolution, mount) runs only when the export is
// genuinely not staged yet, e.g. a pod rescheduled after an in-place upgrade from a
// driver that never staged.
func (d *Node) ensureStaged(
	ctx context.Context,
	stagingTargetPath, volumeID string,
	volumeCapability *csi.VolumeCapability,
	volumeContext map[string]string,
) error {
	staged, err := d.Mounter.IsMountPoint(stagingTargetPath)
	switch {
	case err == nil:
		if staged {
			return nil
		}
	case os.IsNotExist(err):
		// Staging dir not created yet; stageMount's MkdirAll makes it below.
	default:
		return fmt.Errorf("failed to check staging path %s: %w", stagingTargetPath, err)
	}

	nfsEnabled, err := crusoe.GetNFSFlag(ctx, d.CrusoeHTTPClient, d.CrusoeAPIEndpoint, d.HostInstance.ProjectId)
	if err != nil {
		return fmt.Errorf("%s: %w", node.ErrFailedToFetchNFSFlag, err)
	}
	klog.Infof("NFS enabled: %v", nfsEnabled)

	nfsHost, nfsRemotePorts := d.resolveNFSTarget(ctx, volumeID, nfsEnabled)

	return stageVolume(d.Mounter, nfsEnabled, nfsRemotePorts, nfsHost,
		stagingTargetPath, volumeID, volumeCapability, volumeContext)
}

// stageForPublish runs the stage step of NodePublishVolume under the staging-path
// lock, the same key NodeStageVolume uses, so a concurrent stage and this
// publish-driven stage cannot both mount. It returns codes.Aborted when the lock
// is already held.
//
// It is its own method so the release stays deferred: a manual release after
// ensureStaged would leak the lock if ensureStaged panicked, wedging every later
// stage and publish for the volume in Aborted. The defer also fires before the
// caller binds, so the lock is held only across the stage, not the bind. Lock
// order is the caller's target lock then this staging lock; NodeStageVolume only
// ever takes the staging lock, so there is no cycle and no deadlock.
func (d *Node) stageForPublish(
	ctx context.Context,
	stagingTargetPath, volumeID string,
	volumeCapability *csi.VolumeCapability,
	volumeContext map[string]string,
) error {
	if !d.VolumeLocks.TryAcquire(stagingTargetPath) {
		klog.Warningf("operation already in progress for staging target path %s, returning Aborted", stagingTargetPath)

		return status.Errorf(codes.Aborted, node.VolumeOperationAlreadyExistsFmt, stagingTargetPath)
	}
	defer d.VolumeLocks.Release(stagingTargetPath)

	//nolint:wrapcheck // caller maps this to a gRPC status; wrapping here would double-wrap
	return d.ensureStaged(ctx, stagingTargetPath, volumeID, volumeCapability, volumeContext)
}

// NodeUnstageVolume unmounts the per-node staging mount. The CO calls this only
// after every NodeUnpublishVolume for the volume on the node has returned success,
// i.e. when no pod on the node uses the volume, so this is the rare, real umount /
// superblock teardown, with nothing queued behind it.
func (d *Node) NodeUnstageVolume(_ context.Context, request *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse,
	error,
) {
	klog.Infof("Received request to unstage volume: %+v", request)

	stagingTargetPath := request.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%s: NodeUnstageVolume", node.ErrVolumePathEmpty)
	}

	// Same key as NodeStageVolume, so stage and unstage for one staging path cannot
	// interleave.
	if !d.VolumeLocks.TryAcquire(stagingTargetPath) {
		klog.Warningf("operation already in progress for staging target path %s, returning Aborted", stagingTargetPath)

		return nil, status.Errorf(codes.Aborted, node.VolumeOperationAlreadyExistsFmt, stagingTargetPath)
	}
	defer d.VolumeLocks.Release(stagingTargetPath)

	// CleanupMountPoint is a noop when nothing is mounted, so unstage is idempotent.
	err := mount.CleanupMountPoint(stagingTargetPath, d.Mounter, false)
	if err != nil {
		klog.Errorf("failed to unstage volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to unstage volume %s: %s",
			request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully unstaged volume: %s", request.GetVolumeId())

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume bind-mounts the staged volume into the pod's target path. It
// ensures the export is staged first (via ensureStaged), so a pod gets a real
// mount even when the CO skipped NodeStageVolume. That happens after an in-place
// upgrade from a driver that did not stage: kubelet still treats the volume as
// device-mounted for a pod published before the upgrade, so it never calls
// NodeStageVolume for a pod rescheduled onto the node, and a bind of the empty
// staging dir would give that pod an empty mount (CRUSOE-103809). In the common
// case the export is already staged and this is a cheap bind.
func (d *Node) NodePublishVolume(ctx context.Context, request *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	// The staging path is where ensureStaged mounts the export and what the bind
	// clones from, so it is required. kubelet always sends it with
	// STAGE_UNSTAGE_VOLUME advertised; an empty value is a malformed request.
	stagingTargetPath := request.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%s: NodePublishVolume", node.ErrVolumePathEmpty)
	}

	// Serialise on the target path, not the volume ID. A shared-FS volume is
	// published to a separate target path per pod and the CSI spec allows those
	// calls to run concurrently, so locking per volume would refuse legitimate
	// work. Locking per target closes the actual race: kubelet re-drives
	// NodePublishVolume for the same target when its deadline expires.
	//
	// csi-driver-nfs keys the same lock on volumeID + "-" + targetPath. The
	// target path already carries the PV name, which is one to one with a volume,
	// so both keys partition the same way and the prefix adds nothing here.
	targetPath := request.GetTargetPath()
	if !d.VolumeLocks.TryAcquire(targetPath) {
		klog.Warningf("operation already in progress for target path %s, returning Aborted", targetPath)

		return nil, status.Errorf(codes.Aborted, node.VolumeOperationAlreadyExistsFmt, targetPath)
	}
	defer d.VolumeLocks.Release(targetPath)

	// Ensure the export is staged before binding, under the staging-path lock and
	// released before the bind (see stageForPublish).
	if stageErr := d.stageForPublish(ctx, stagingTargetPath, request.GetVolumeId(),
		request.GetVolumeCapability(), request.GetVolumeContext()); stageErr != nil {
		// A held staging lock is a concurrent stage in flight: surface Aborted so the
		// CO retries, rather than reporting it as an internal failure.
		if status.Code(stageErr) == codes.Aborted {
			return nil, stageErr
		}
		klog.Errorf("failed to stage volume %s during publish: %s", request.GetVolumeId(), stageErr.Error())

		return nil, status.Errorf(codes.Internal,
			"failed to stage volume %s during publish: %s", request.GetVolumeId(), stageErr.Error())
	}

	err := nodePublishVolumeBind(d.Mounter, request)
	if err != nil {
		klog.Errorf("failed to publish volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to publish volume %s: %s", request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully published volume: %s", request.GetVolumeId())

	return &csi.NodePublishVolumeResponse{}, nil
}

// resolveNFSTarget determines the NFS host and remoteports value to use when
// publishing a volume.
//
// The entire userspace-resolution path is gated behind a single feature flag.
// When the flag is OFF — or unavailable — behaviour is identical to the released
// driver: legacyResolveNFSTarget emits the target (possibly the literal "dns")
// and the kernel resolves it via the dns_resolver keyring upcall.
//
// When the flag is ON, we prefer the per-disk target (Vips absolutely, else
// DnsName) and resolve it in-process so the kernel never receives "dns" — this
// avoids the keyring upcall and the resolver failure modes it can hit (an ENOKEY
// race between concurrent mounts, and EPROTONOSUPPORT / refused-AAAA failures
// from an unspecified-IPv6 answer). Any failure or timeout in the new path falls
// back wholesale to legacyResolveNFSTarget, so a resolver problem is never worse
// than today's behaviour.
func (d *Node) resolveNFSTarget(
	ctx context.Context, volumeID string, nfsEnabled bool,
) (nfsHost, nfsRemotePorts string) {
	disk := d.fetchDiskOrNil(ctx, volumeID, nfsEnabled)

	if !d.userspaceDNSResolutionEnabled(ctx) {
		return d.legacyResolveNFSTarget(ctx, disk)
	}

	// FF on: prefer the per-disk target (Vips-first). If there is no usable
	// per-disk target, the legacy result is both the raw source and the
	// fallback, so resolve it once and reuse it.
	rawHost, rawRemotePorts, fromVips := "", "", false
	if disk != nil {
		rawHost, rawRemotePorts, fromVips = crusoe.ResolveNFSTarget(disk)
	}
	if !fromVips {
		rawHost, rawRemotePorts = d.legacyResolveNFSTarget(ctx, disk)
	}

	// Either target may be a "dns" value; materialize it so the kernel never
	// performs the dns_resolver upcall.
	newHost, newRemotePorts, err := materializeNFSTarget(ctx, rawHost, rawRemotePorts)
	if err != nil {
		klog.Warningf("userspace NFS resolution failed for volume %s (host=%s remoteports=%s), "+
			"falling back to legacy behaviour: %s", volumeID, rawHost, rawRemotePorts, err.Error())

		if fromVips {
			// Raw target came from Vips; fall back to the legacy result.
			return d.legacyResolveNFSTarget(ctx, disk)
		}

		// Raw target already is the legacy result; reuse it without re-fetching
		// the feature flag.
		return rawHost, rawRemotePorts
	}

	klog.Infof("Resolved NFS target (userspace) for %s: host=%s remoteports=%s (raw host=%s remoteports=%s)",
		volumeID, newHost, newRemotePorts, rawHost, rawRemotePorts)

	return newHost, newRemotePorts
}

// fetchDiskOrNil returns the disk for volumeID, or nil if NFS is disabled, the
// volumeID is empty, or the lookup fails. A nil disk drives resolution to the
// configured defaults.
func (d *Node) fetchDiskOrNil(
	ctx context.Context, volumeID string, nfsEnabled bool,
) *crusoeapi.DiskV1Alpha5 {
	if !nfsEnabled || volumeID == "" {
		return nil
	}
	disk, err := crusoe.FindDiskByIDFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, volumeID)
	if err != nil {
		klog.Warningf("failed to fetch disk %s for NFS target resolution: %s", volumeID, err.Error())

		return nil
	}

	return disk
}

// userspaceDNSResolutionEnabled reports whether the project has opted into
// CSI-side NFS DNS resolution. It defaults to false (legacy behaviour) on any
// flag-fetch error, so an unreachable or not-yet-deployed flag endpoint keeps
// today's behaviour.
func (d *Node) userspaceDNSResolutionEnabled(ctx context.Context) bool {
	enabled, err := crusoe.GetUserspaceDNSResolutionFlag(
		ctx, d.CrusoeHTTPClient, d.CrusoeAPIEndpoint, d.HostInstance.ProjectId)
	if err != nil {
		klog.Warningf("failed to fetch userspace-DNS-resolution flag, defaulting to legacy resolution: %s",
			err.Error())

		return false
	}

	return enabled
}

// legacyResolveNFSTarget reproduces the previously-released resolution exactly:
// DnsName-first per-disk resolution, then the configured CLI-flag defaults or
// the secondary-cluster DNS fallback. It performs NO userspace materialization
// — a "dns" remoteports value is handed to the kernel as-is. This is the
// behaviour the FF-off path and every failure/timeout fall back to.
func (d *Node) legacyResolveNFSTarget(
	ctx context.Context, disk *crusoeapi.DiskV1Alpha5,
) (nfsHost, nfsRemotePorts string) {
	if disk != nil {
		if host, remotePorts, ok := crusoe.ResolveNFSTargetLegacy(disk); ok {
			klog.Infof("Resolved NFS target (legacy) from disk API: host=%s remoteports=%s", host, remotePorts)

			return host, remotePorts
		}
	}

	nfsHost = d.NFSHost
	nfsRemotePorts = d.NFSRemotePorts
	klog.Infof("Host instance location: %q, DNS-fallback location: %q", d.HostInstance.Location, dnsFallbackLocation)
	if d.useDNSForMount(ctx) {
		klog.Warningf("falling back to DNS-based NFS host: %s", crusoeCloudDNSNFSHost)
		nfsHost = crusoeCloudDNSNFSHost
		nfsRemotePorts = dnsRemotePorts
	} else {
		klog.Warningf("falling back to configured IP-based NFS host: %s with remote ports: %s",
			nfsHost, nfsRemotePorts)
	}

	return nfsHost, nfsRemotePorts
}

func (d *Node) useDNSForMount(ctx context.Context) bool {
	useSecondaryVast, err := crusoe.GetVastUseSecondaryClusterFlag(
		ctx, d.CrusoeHTTPClient, d.CrusoeAPIEndpoint, d.HostInstance.ProjectId)
	if err != nil {
		klog.Errorf("failed to fetch VastUseSecondaryCluster flag: %s", err.Error())

		return false
	}

	return useSecondaryVast && d.HostInstance.Location == dnsFallbackLocation
}

func (d *Node) NodeUnpublishVolume(_ context.Context, request *csi.NodeUnpublishVolumeRequest) (
	*csi.NodeUnpublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to unpublish volume: %+v", request)

	// Same key as NodePublishVolume, so publish and unpublish for one target
	// cannot interleave. Without this an unpublish can tear down a mount a
	// concurrent publish just completed.
	targetPath := request.GetTargetPath()
	if !d.VolumeLocks.TryAcquire(targetPath) {
		klog.Warningf("operation already in progress for target path %s, returning Aborted", targetPath)

		return nil, status.Errorf(codes.Aborted, node.VolumeOperationAlreadyExistsFmt, targetPath)
	}
	defer d.VolumeLocks.Release(targetPath)

	err := mount.CleanupMountPoint(targetPath, d.Mounter, false)
	if err != nil {
		klog.Errorf("failed to cleanup mount point for volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to cleanup mount point for volume %s: %s",
			request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully unpublished volume: %s", request.GetVolumeId())

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *Node) NodeGetVolumeStats(_ context.Context, req *csi.NodeGetVolumeStatsRequest) (
	*csi.NodeGetVolumeStatsResponse,
	error,
) {
	//nolint:wrapcheck // error is already a gRPC status; wrapping would lose the status code
	return node.GetVolumeStats(req)
}

// NodeExpandVolume This function is currently unused.
// common.DiskTypeFS disks do not require expansion on the node.
// common.DiskTypeSSD disks would require expansion on the node if they supported online expansion.
func (d *Node) NodeExpandVolume(_ context.Context, _ *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse,
	error,
) {
	klog.Errorf("%s: NodeGetVolumeStats", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeGetVolumeStats", common.ErrNotImplemented)
}

func (d *Node) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse,
	error,
) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: d.Capabilities,
	}, nil
}

func (d *Node) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	//nolint:lll // long names
	topologySegments := map[string]string{
		common.GetTopologyKey(d.PluginName, common.TopologyLocationKey):            d.HostInstance.Location,
		common.GetTopologyKey(d.PluginName, common.TopologySupportsSharedDisksKey): strconv.FormatBool(supportsFS(d.HostInstance)),
	}

	return &csi.NodeGetInfoResponse{
		NodeId:            d.HostInstance.Id,
		MaxVolumesPerNode: d.MaxVolumesPerNode,
		AccessibleTopology: &csi.Topology{
			Segments: topologySegments,
		},
	}, nil
}
