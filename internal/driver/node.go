package driver

import (
	"context"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
	"k8s.io/utils/exec"
)

// MaxVolumesPerNode refers  to the maximum number of disks that can be attached to a VM
// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
const MaxVolumesPerNode = 16
const TopologyLocationKey = "location"  // TODO: figure out if this is the right key
const TopologyProjectKey = "project-id" // TODO: figure out if this is the right key
const VolumeContextDiskSerialNumberKey = "serial-number"
const ReadOnlyMountOption = "ro"

var NodeServerCapabilities = []csi.NodeServiceCapability_RPC_Type{
	csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
}

type NodeServer struct {
	apiClient *crusoeapi.APIClient
	driver    *DriverConfig
	mounter   *mount.SafeFormatAndMount
}

func NewNodeServer() *NodeServer {
	return &NodeServer{}
}

func (n *NodeServer) Init(apiClient *crusoeapi.APIClient, driver *DriverConfig, _ []Service) error {
	n.driver = driver
	n.apiClient = apiClient
	n.mounter = mount.NewSafeFormatAndMount(mount.New(""), exec.New())

	return nil
}

func (n *NodeServer) RegisterServer(srv *grpc.Server) error {
	csi.RegisterNodeServer(srv, n)

	return nil
}

func (n *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (n *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	stagingTargetPath := req.GetStagingTargetPath()
	readOnly := req.GetReadonly()

	volumeCapability := req.GetVolumeCapability()

	var sourcePath string
	// assume we are ext4
	fsType := "ext4"
	// options to be passed to the mount syscall (see https://linux.die.net/man/8/mount for more info on options)
	opts := make([]string, 0)
	if readOnly {
		opts = append(opts, ReadOnlyMountOption)
	}

	// symlink: /dev/disk/by-id/virtio-07EB48176D5521A3EA6
	if volumeCapability.GetBlock() != nil {
		volumeContext := req.GetVolumeContext()
		serialNumber, ok := volumeContext[VolumeContextDiskSerialNumberKey]
		if !ok {
			return nil, status.Errorf(codes.FailedPrecondition, fmt.Sprintf("volume missing serial number context key"))
		}
		devicePath := fmt.Sprintf("/dev/disk/by-id/virtio-%s", serialNumber)
		sourcePath = devicePath
	} else if volumeCapability.GetMount() != nil {
		sourcePath = stagingTargetPath
		fsType = volumeCapability.GetMount().GetFsType()
		opts = append(opts, volumeCapability.GetMount().GetMountFlags()...)
	}

	err := n.mounter.Mount(sourcePath, targetPath, fsType, opts)
	if err != nil {
		// TODO: not every error is bad, sometimes if the volume is already mounted this will return error
		// however, in those cases we want to return success
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to mount volume at target path: %s", err.Error()))
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	err := mount.CleanupMountPoint(targetPath, n.mounter, false)
	if err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to cleanup mount point %s", err.Error()))
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return &csi.NodeGetVolumeStatsResponse{}, nil
}

func (n *NodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return &csi.NodeExpandVolumeResponse{}, nil
}

func (n *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	nodeCapabilities := make([]*csi.NodeServiceCapability, 0, len(NodeServerCapabilities))

	for _, capability := range NodeServerCapabilities {
		nodeCapabilities = append(nodeCapabilities, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: nodeCapabilities,
	}, nil
}

func (n *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	// We want to provide useful topological hints to the container orchestrator
	// We can only stage/publish volumes in the same location as a node
	accessibleTopology := &csi.Topology{
		Segments: map[string]string{
			TopologyLocationKey: n.driver.GetNodeLocation(),
			TopologyProjectKey:  n.driver.GetNodeProject(),
		},
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             n.driver.GetNodeID(),
		MaxVolumesPerNode:  MaxVolumesPerNode,
		AccessibleTopology: accessibleTopology,
	}, nil
}
