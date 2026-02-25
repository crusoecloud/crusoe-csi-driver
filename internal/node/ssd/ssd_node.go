package ssd

import (
	"context"
	"net/http"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

type Node struct {
	csi.UnimplementedNodeServer
	CrusoeClient      *crusoeapi.APIClient
	CrusoeHTTPClient  *http.Client
	HostInstance      *crusoeapi.InstanceV1Alpha5
	Mounter           *mount.SafeFormatAndMount
	Resizer           *mount.ResizeFs
	CrusoeAPIEndpoint string
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

func (d *Node) NodePublishVolume(_ context.Context, request *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	var mountOpts []string

	if request.GetReadonly() {
		// Read-only volumes cannot be written to in any way
		// We should not attempt to replay the journal
		mountOpts = append(mountOpts, node.ReadOnlyMountOption, node.NoLoadMountOption)
	}

	err := nodePublishVolume(d.Mounter, d.Resizer, mountOpts, request)
	if err != nil {
		klog.Errorf("failed to publish volume %s: %s", request.GetVolumeId(), err.Error())

		return nil, status.Errorf(codes.Internal, "failed to publish volume %s: %s", request.GetVolumeId(), err.Error())
	}

	klog.Infof("Successfully published volume: %s", request.GetVolumeId())

	return &csi.NodePublishVolumeResponse{}, nil
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

func (d *Node) NodeGetVolumeStats(_ context.Context, request *csi.NodeGetVolumeStatsRequest) (
	*csi.NodeGetVolumeStatsResponse,
	error,
) {
	if request.GetVolumeId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "volume ID must be provided")
	}

	if request.GetVolumePath() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "volume path must be provided")
	}

	if _, err := os.Stat(request.GetVolumePath()); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "volume path %s does not exist", request.GetVolumePath())
	}

	isBlock, err := node.IsBlockDevice(request.GetVolumePath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to determine volume type: %s", err)
	}

	var usage []*csi.VolumeUsage
	if isBlock {
		usage, err = node.GetBlockDeviceStats(request.GetVolumePath())
	} else {
		usage, err = node.GetFilesystemStats(request.GetVolumePath())
	}

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get volume stats: %s", err)
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: usage,
	}, nil
}

// NodeExpandVolume This function is currently unused.
// common.DiskTypeFS disks do not require expansion on the node.
// common.DiskTypeSSD disks would require expansion on the node if they supported online expansion.
func (d *Node) NodeExpandVolume(_ context.Context, _ *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse,
	error,
) {
	klog.Errorf("%s: NodeExpandVolume", common.ErrNotImplemented)

	return nil, status.Errorf(codes.Unimplemented, "%s: NodeExpandVolume", common.ErrNotImplemented)
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
	topologySegments := map[string]string{
		common.GetTopologyKey(d.PluginName, common.TopologyLocationKey): d.HostInstance.Location,
	}

	return &csi.NodeGetInfoResponse{
		NodeId:            d.HostInstance.Id,
		MaxVolumesPerNode: d.MaxVolumesPerNode,
		AccessibleTopology: &csi.Topology{
			Segments: topologySegments,
		},
	}, nil
}
