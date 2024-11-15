package driver

import (
	"context"
	"errors"
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
	"k8s.io/utils/exec"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

const (
	// MaxVolumesPerNode refers  to the maximum number of disks that can be attached to a VM
	// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
	MaxVolumesPerNode                = 16
	TopologyLocationKey              = "topology.csi.crusoe.ai/location"
	TopologyProjectKey               = "topology.csi.crusoe.ai/project-id"
	VolumeContextDiskSerialNumberKey = "serial-number"
	VolumeContextDiskTypeKey         = "disk-type"
	ReadOnlyMountOption              = "ro"
	newDirPerms                      = 0o755 // this represents: rwxr-xr-x
	newFilePerms                     = 0o644 // this represents: rw-r--r--
)

var errVolumeMissingSerialNumber = errors.New("volume missing serial number context key")

//nolint:gochecknoglobals // we will use this slice to determine what the node service supports
var NodeServerCapabilities = []csi.NodeServiceCapability_RPC_Type{
	csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
}

type NodeServer struct {
	apiClient *crusoeapi.APIClient
	driver    *Config
	mounter   *mount.SafeFormatAndMount
}

func NewNodeServer() *NodeServer {
	return &NodeServer{}
}

func (n *NodeServer) Init(apiClient *crusoeapi.APIClient, driver *Config, _ []Service) error {
	n.driver = driver
	n.apiClient = apiClient
	n.mounter = mount.NewSafeFormatAndMount(mount.New(""), exec.New())

	return nil
}

func (n *NodeServer) RegisterServer(srv *grpc.Server) error {
	csi.RegisterNodeServer(srv, n)

	return nil
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (n *NodeServer) NodeStageVolume(_ context.Context,
	_ *csi.NodeStageVolumeRequest,
) (*csi.NodeStageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (n *NodeServer) NodeUnstageVolume(_ context.Context,
	_ *csi.NodeUnstageVolumeRequest,
) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

func (n *NodeServer) NodePublishVolume(_ context.Context,
	req *csi.NodePublishVolumeRequest,
) (*csi.NodePublishVolumeResponse, error) {
	klog.Infof("Received request to publish volume: %+v", req)
	targetPath := req.GetTargetPath()
	readOnly := req.GetReadonly()

	volumeCapability := req.GetVolumeCapability()

	var mountOpts []string

	if readOnly {
		mountOpts = append(mountOpts, ReadOnlyMountOption)
	}

	// Check if volume is already mounted, if it is return success
	mounted, err := n.mounter.IsMountPoint(targetPath)
	if err == nil && mounted {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if volumeCapability.GetBlock() != nil {
		mountErr := publishBlockVolume(req, targetPath, n.mounter, mountOpts)
		if mountErr != nil {
			return nil, status.Errorf(codes.Internal, "failed to mount block volume: %s", mountErr.Error())
		}
	} else if volumeCapability.GetMount() != nil {
		mountErr := publishFilesystemVolume(req, targetPath, n.mounter, mountOpts)
		if mountErr != nil {
			return nil, status.Errorf(codes.Internal, "failed to mount filesystem volume: %s", mountErr.Error())
		}
	}

	klog.Infof("Successfully published volume: %s", req.GetVolumeId())

	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(_ context.Context,
	req *csi.NodeUnpublishVolumeRequest,
) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("Received request to unpublish volume: %+v", req)

	targetPath := req.GetTargetPath()
	err := mount.CleanupMountPoint(targetPath, n.mounter, false)
	if err != nil {
		return nil, status.Errorf(codes.Internal, fmt.Sprintf("failed to cleanup mount point %s", err.Error()))
	}

	klog.Infof("Successfully unpublished volume: %s", req.GetVolumeId())

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (n *NodeServer) NodeGetVolumeStats(_ context.Context,
	_ *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (n *NodeServer) NodeExpandVolume(_ context.Context,
	_ *csi.NodeExpandVolumeRequest,
) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

func (n *NodeServer) NodeGetCapabilities(_ context.Context,
	_ *csi.NodeGetCapabilitiesRequest,
) (*csi.NodeGetCapabilitiesResponse, error) {
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

func (n *NodeServer) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
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
