package node

import (
	"context"
	"errors"
	"fmt"
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

var ErrFailedResize = errors.New("failed to resize disk")

type DefaultNode struct {
	csi.UnimplementedNodeServer
	CrusoeClient  *crusoeapi.APIClient
	HostInstance  *crusoeapi.InstanceV1Alpha5
	Mounter       *mount.SafeFormatAndMount
	Resizer       *mount.ResizeFs
	DiskType      common.DiskType
	PluginName    string
	PluginVersion string
	Capabilities  []*csi.NodeServiceCapability
}

func (d *DefaultNode) NodeStageVolume(_ context.Context, _ *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: NodeStageVolume", common.ErrNotImplemented)
}

func (d *DefaultNode) NodeUnstageVolume(_ context.Context, _ *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: NodeUnstageVolume", common.ErrNotImplemented)
}

func (d *DefaultNode) NodePublishVolume(_ context.Context, request *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	var mountOpts []string

	if request.GetReadonly() {
		mountOpts = append(mountOpts, readOnlyMountOption)
		mountOpts = append(mountOpts, noLoadMountOption)
	}

	err := nodePublishVolume(d.Mounter, d.Resizer, mountOpts, d.DiskType, request)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to publish volume: %s", err.Error())
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
		return nil, status.Errorf(codes.Internal, "failed to cleanup mount point %s", err.Error())
	}

	klog.Infof("Successfully unpublished volume: %s", request.GetVolumeId())

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *DefaultNode) NodeGetVolumeStats(_ context.Context, _ *csi.NodeGetVolumeStatsRequest) (
	*csi.NodeGetVolumeStatsResponse,
	error,
) {
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
		return nil, status.Errorf(codes.NotFound, "failed to find disk: %s", err)
	}
	devicePath := getSSDDevicePath(disk.SerialNumber)

	ok, err := d.Resizer.Resize(devicePath, request.GetVolumePath())
	if err != nil {
		return nil, fmt.Errorf("failed to resize %s: %w", request.GetVolumePath(), err)
	}

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFailedResize, request.GetVolumePath())
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
		NodeId: d.HostInstance.Id,
		// Hard limit upstream, change if needed
		// Subtract 1 to allow for the boot disk
		MaxVolumesPerNode: common.MaxVolumesPerNode - 1,
		AccessibleTopology: &csi.Topology{
			Segments: topologySegments,
		},
	}, nil
}
