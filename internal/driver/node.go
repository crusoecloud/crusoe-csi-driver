package driver

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"google.golang.org/grpc"
)

// MaxVolumesPerNode refers  to the maximum number of disks that can be attached to a VM
// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
const MaxVolumesPerNode = 16
const TopologyLocationKey = "location" // TODO: figure out if this is the right key

var NodeServerCapabilities = []csi.NodeServiceCapability_RPC_Type{
	csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
	csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
	csi.NodeServiceCapability_RPC_VOLUME_MOUNT_GROUP,
}

type NodeServer struct {
	apiClient *crusoeapi.APIClient
	driver    *DriverConfig
}

func NewNodeServer() *NodeServer {
	return &NodeServer{}
}

func (n *NodeServer) Init(apiClient *crusoeapi.APIClient, driver *DriverConfig) error {
	n.driver = driver
	n.apiClient = apiClient

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
	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
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
		},
	}

	return &csi.NodeGetInfoResponse{
		NodeId:             n.driver.GetNodeIdentifier(), // TODO: get node id from driver
		MaxVolumesPerNode:  MaxVolumesPerNode,
		AccessibleTopology: accessibleTopology,
	}, nil
}
