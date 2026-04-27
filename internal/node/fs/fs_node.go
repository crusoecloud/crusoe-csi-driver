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
	icatLocation          = "eu-iceland1-a"
	dnsRemotePorts        = "dns"
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
// publishing a volume. It prefers per-disk data path connectivity returned by
// the storage API (dns_name / vips, CRUSOE-60428), falling back to legacy
// configuration (the ICAT secondary-cluster DNS escape hatch and finally the
// CLI-flag defaults) when the API does not yet populate those fields.
func (d *Node) resolveNFSTarget(
	ctx context.Context, volumeID string, nfsEnabled bool,
) (nfsHost, nfsRemotePorts string) {
	if nfsEnabled && volumeID != "" {
		disk, err := crusoe.FindDiskByIDFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, volumeID)
		if err != nil {
			klog.Warningf("failed to fetch disk %s for NFS target resolution, falling back to defaults: %s",
				volumeID, err.Error())
		} else if host, remotePorts, ok := crusoe.ResolveNFSTarget(disk); ok {
			klog.Infof("Resolved NFS target from disk API for %s: host=%s remoteports=%s",
				volumeID, host, remotePorts)

			return host, remotePorts
		} else {
			klog.Warningf("disk %s did not return data path connectivity fields; falling back to defaults",
				volumeID)
		}
	}

	nfsHost = d.NFSHost
	nfsRemotePorts = d.NFSRemotePorts
	klog.Infof("Host instance location: %q, checking against icatLocation: %q", d.HostInstance.Location, icatLocation)
	if d.useDNSForMount(ctx) {
		klog.Warningf("falling back to ICAT DNS-based NFS host: %s", crusoeCloudDNSNFSHost)
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

	return useSecondaryVast && d.HostInstance.Location == icatLocation
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
