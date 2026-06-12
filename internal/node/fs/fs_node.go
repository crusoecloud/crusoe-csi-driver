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
	CrusoeClient      *crusoeapi.APIClient
	CrusoeHTTPClient  *http.Client
	HostInstance      *crusoeapi.InstanceV1Alpha5
	Mounter           *mount.SafeFormatAndMount
	Resizer           *mount.ResizeFs
	CrusoeAPIEndpoint string
	NFSRemotePorts    string
	NFSHost           string
	DiskType          common.DiskType
	PluginName        string
	PluginVersion     string
	Capabilities      []*csi.NodeServiceCapability
	MaxVolumesPerNode int64
}

func (d *Node) NodeStageVolume(_ context.Context, _ *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse,
	error,
) {
	klog.Errorf("%s: NodeStageVolume", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeStageVolume", common.ErrNotImplemented)
}

func (d *Node) NodeUnstageVolume(_ context.Context, _ *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse,
	error,
) {
	klog.Errorf("%s: NodeUnstageVolume", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeUnstageVolume", common.ErrNotImplemented)
}

func (d *Node) NodePublishVolume(ctx context.Context, request *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	nfsEnabled, err := crusoe.GetNFSFlag(ctx, d.CrusoeHTTPClient, d.CrusoeAPIEndpoint, d.HostInstance.ProjectId)
	if err != nil {
		klog.Errorf("%s: %s", node.ErrFailedToFetchNFSFlag, err)

		return nil, status.Errorf(codes.Internal, "%s: %s", node.ErrFailedToFetchNFSFlag, err)
	}
	klog.Infof("NFS enabled: %v", nfsEnabled)

	var mountOpts []string

	if request.GetReadonly() {
		// Read-only volumes cannot be written to in any way
		mountOpts = append(mountOpts, node.ReadOnlyMountOption)
	}

	nfsHost, nfsRemotePorts := d.resolveNFSTarget(ctx, request.GetVolumeId(), nfsEnabled)

	err = nodePublishVolume(d.Mounter, d.Resizer, mountOpts, nfsEnabled, nfsRemotePorts, nfsHost, request)
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

	targetPath := request.GetTargetPath()
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
