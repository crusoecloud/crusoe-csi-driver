package fs

import (
	"context"
	"net/http"
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

	nfsEnabled, err := crusoe.GetNFSFlag(ctx, d.CrusoeHTTPClient, d.CrusoeAPIEndpoint, d.HostInstance.ProjectId)
	if err != nil {
		klog.Errorf("%s: %s", node.ErrFailedToFetchNFSFlag, err)

		return nil, status.Errorf(codes.Internal, "%s: %s", node.ErrFailedToFetchNFSFlag, err)
	}
	klog.Infof("NFS enabled: %v", nfsEnabled)

	nfsHost, nfsRemotePorts := d.resolveNFSTarget(ctx, request.GetVolumeId(), nfsEnabled)

	err = nodeStageVolume(d.Mounter, nfsEnabled, nfsRemotePorts, nfsHost, request)
	if err != nil {
		klog.Errorf("failed to stage volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to stage volume %s: %s", request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully staged volume: %s", request.GetVolumeId())

	return &csi.NodeStageVolumeResponse{}, nil
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

// NodePublishVolume bind-mounts the already-staged volume into the pod's target
// path. The expensive export mount happens once in NodeStageVolume; publish is a
// cheap bind, so a pod restart is unbind + rebind against a mount that stays put.
func (d *Node) NodePublishVolume(_ context.Context, request *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	// With STAGE_UNSTAGE_VOLUME advertised the CO must call NodeStageVolume first,
	// so a publish without a staging path is a CO/programmer error, not something
	// to paper over by re-mounting the export here.
	if request.GetStagingTargetPath() == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"staging target path must be provided; NodeStageVolume must run before NodePublishVolume")
	}

	// Serialise on the target path, not the volume ID. A shared-FS volume is
	// published to a separate target path per pod and the CSI spec allows those
	// calls to run concurrently, so locking per volume would refuse legitimate
	// work. Locking per target closes the actual race: kubelet re-drives
	// NodePublishVolume for the same target when its deadline expires. The bind is
	// cheap now, but the guard is kept because the re-drive still happens.
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
