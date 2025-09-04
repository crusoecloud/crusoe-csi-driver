package node

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

type DefaultNode struct {
	csi.UnimplementedNodeServer
	CrusoeClient      *crusoeapi.APIClient
	CrusoeHTTPClient  *http.Client
	CrusoeAPIEndpoint string
	HostInstance      *crusoeapi.InstanceV1Alpha5
	Mounter           *mount.SafeFormatAndMount
	Resizer           *mount.ResizeFs
	DiskType          common.DiskType
	PluginName        string
	PluginVersion     string
	Capabilities      []*csi.NodeServiceCapability
	MaxVolumesPerNode int64
}

func (d *DefaultNode) NodeStageVolume(_ context.Context, _ *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse,
	error,
) {
	klog.Errorf("%s: NodeStageVolume", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeStageVolume", common.ErrNotImplemented)
}

func (d *DefaultNode) NodeUnstageVolume(_ context.Context, _ *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse,
	error,
) {
	klog.Errorf("%s: NodeUnstageVolume", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeUnstageVolume", common.ErrNotImplemented)
}

func (d *DefaultNode) NodePublishVolume(_ context.Context, request *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	var mountOpts []string

	if request.GetReadonly() {
		// Read-only volumes cannot be written to in any way
		// We should not attempt to replay the journal
		mountOpts = append(mountOpts, readOnlyMountOption, noLoadMountOption)
	}

	nfsEnabled := false

	if d.DiskType == common.DiskTypeFS {
		var err error
		nfsEnabled, err = crusoe.GetNFSFlag(d.CrusoeHTTPClient, d.CrusoeAPIEndpoint, d.HostInstance.ProjectId)
		if err != nil {
			klog.Errorf("%s: %s", ErrFailedToFetchNFSFlag, err)

			return nil, status.Errorf(codes.Internal, "%s: %s", ErrFailedToFetchNFSFlag, err)
		}
	}

	err := nodePublishVolume(d.Mounter, d.Resizer, mountOpts, d.DiskType, nfsEnabled, request)
	if err != nil {
		klog.Errorf("failed to publish volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to publish volume %s: %s", request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully published volume: %s", request.GetVolumeId())

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *DefaultNode) NodeUnpublishVolume(_ context.Context, request *csi.NodeUnpublishVolumeRequest) (
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

func (d *DefaultNode) NodeGetVolumeStats(_ context.Context, _ *csi.NodeGetVolumeStatsRequest) (
	*csi.NodeGetVolumeStatsResponse,
	error,
) {
	klog.Errorf("%s: NodeGetVolumeStats", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeGetVolumeStats", common.ErrNotImplemented)
}

// NodeExpandVolume This function is currently unused.
// common.DiskTypeFS disks do not require expansion on the node.
// common.DiskTypeSSD disks would require expansion on the node if they supported online expansion.
func (d *DefaultNode) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse,
	error,
) {
	// Note that this function will only be called for diskType == common.DiskTypeSSD
	// because FS disks do not require expansion on the node

	// Block devices do not require expansion on the node
	if request.GetVolumeCapability().GetBlock() != nil {
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	// Fetch disk's serial number because NodeExpandVolumeRequest does not include the volume context :(
	disk, err := crusoe.FindDiskByIDFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, request.GetVolumeId())
	if err != nil {
		klog.Errorf("failed to find disk %s: %s", request.GetVolumeId(), err)

		return nil, status.Errorf(codes.NotFound, "failed to find disk %s: %s", request.GetVolumeId(), err)
	}
	devicePath := getSSDDevicePath(disk.SerialNumber)

	ok, err := d.Resizer.Resize(devicePath, request.GetVolumePath())
	if err != nil {
		return nil, fmt.Errorf("failed to resize %s: %w", request.GetVolumePath(), err)
	}

	if !ok {
		klog.Errorf("%s for volume %s: %s", ErrFailedResize, request.GetVolumePath(), request.GetVolumeId())

		return nil, status.Errorf(codes.Internal, "%s for volume %s: %s",
			ErrFailedResize, request.GetVolumePath(), request.GetVolumeId())
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}

func (d *DefaultNode) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse,
	error,
) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: d.Capabilities,
	}, nil
}

func (d *DefaultNode) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	topologySegments := map[string]string{
		common.GetTopologyKey(d.PluginName, common.TopologyLocationKey): d.HostInstance.Location,
	}

	//nolint:lll // long names
	if d.DiskType == common.DiskTypeFS {
		topologySegments[common.GetTopologyKey(d.PluginName, common.TopologySupportsSharedDisksKey)] = strconv.FormatBool(supportsFS(d.HostInstance))
	}

	return &csi.NodeGetInfoResponse{
		NodeId:            d.HostInstance.Id,
		MaxVolumesPerNode: d.MaxVolumesPerNode,
		AccessibleTopology: &csi.Topology{
			Segments: topologySegments,
		},
	}, nil
}
