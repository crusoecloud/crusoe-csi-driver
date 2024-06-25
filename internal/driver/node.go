package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
	"k8s.io/utils/exec"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

// MaxVolumesPerNode refers  to the maximum number of disks that can be attached to a VM
// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
const (
	MaxVolumesPerNode                = 16
	TopologyLocationKey              = "topology.csi.crusoe.ai/location"
	TopologyProjectKey               = "topology.csi.crusoe.ai/project-id"
	VolumeContextDiskSerialNumberKey = "serial-number"
	VolumeContextDiskTypeKey         = "disk-type"
	ReadOnlyMountOption              = "ro"
	newDirPerms                      = 0o755 // this represents: rwxr-xr-x
	newFilePerms                     = 0o644
)

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
	klog.Infof("Received request to publish volume: %+v", req)
	targetPath := req.GetTargetPath()
	stagingTargetPath := req.GetStagingTargetPath()
	readOnly := req.GetReadonly()

	volumeCapability := req.GetVolumeCapability()

	mountOpts := []string{"bind"}
	if readOnly {
		mountOpts = append(mountOpts, ReadOnlyMountOption)
	}

	// Check if volume is already mounted, if it is return success
	mounted, err := n.mounter.IsMountPoint(targetPath)
	if err == nil && mounted {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if volumeCapability.GetBlock() != nil {
		volumeContext := req.GetVolumeContext()
		serialNumber, ok := volumeContext[VolumeContextDiskSerialNumberKey]
		if !ok {
			return nil, status.Errorf(codes.FailedPrecondition, "volume missing serial number context key")
		}

		devicePath := getPersistentSSDDevicePath(serialNumber)
		dirPath := filepath.Dir(targetPath)
		// Check if the directory exists
		if _, err := os.Stat(dirPath); errors.Is(err, os.ErrNotExist) {
			// Directory does not exist, create it

			if err := os.MkdirAll(dirPath, newDirPerms); err != nil {
				return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to make directory for target path: %s", err.Error()))
			}
		}

		// expose the block volume as a file
		f, err := os.OpenFile(targetPath, os.O_CREATE, os.FileMode(newFilePerms))
		if err != nil {
			if !os.IsExist(err) {
				return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to make file for target path: %s", err.Error()))
			}
		}
		if err = f.Close(); err != nil {
			return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to close file after making target path: %s", err.Error()))
		}

		err = n.mounter.FormatAndMount(devicePath, targetPath, "", mountOpts)
		if err != nil {
			return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to mount volume at target path: %s", err.Error()))
		}
	} else if volumeCapability.GetMount() != nil {
		var sourcePath string
		fsType := volumeCapability.GetMount().GetFsType()
		sourcePath = stagingTargetPath
		mountOpts = append(mountOpts, volumeCapability.GetMount().GetMountFlags()...)
		err := os.MkdirAll(sourcePath, newDirPerms)
		if err != nil {
			return nil, err
		}
		err = n.mounter.Mount(sourcePath, targetPath, fsType, mountOpts)
		if err != nil {
			return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to mount volume at target path: %s", err.Error()))
		}
	}

	klog.Infof("Successfully published volume: %s", req.GetVolumeId())

	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("Received request to unpublish volume: %+v", req)

	targetPath := req.GetTargetPath()
	err := mount.CleanupMountPoint(targetPath, n.mounter, false)
	if err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to cleanup mount point %s", err.Error()))
	}

	klog.Infof("Successfully unpublished volume: %s", req.GetVolumeId())

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
