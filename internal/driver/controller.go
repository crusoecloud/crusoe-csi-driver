package driver

import (
	"context"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ControllerServerCapabilities = []csi.ControllerServiceCapability_RPC_Type{
	// TODO: figure out what capabilities we need to support
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
	csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
}

type ControllerServer struct {
	apiClient *crusoeapi.APIClient
	driver    *DriverConfig
}

func NewControllerServer() *ControllerServer {
	return &ControllerServer{}
}

func (c *ControllerServer) Init(apiClient *crusoeapi.APIClient, driver *DriverConfig) error {
	c.driver = driver
	c.apiClient = apiClient

	return nil
}

func (c *ControllerServer) RegisterServer(srv *grpc.Server) error {
	csi.RegisterControllerServer(srv, c)

	return nil
}

func (c *ControllerServer) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	capabilities := request.GetVolumeCapabilities()
	if capErr := validateVolumeCapabilities(capabilities); capErr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume capabilities: %w", capErr)
	}

	capacityRange := request.GetCapacityRange()
	// Note: both RequiredBytes and LimitBytes SHOULD be set to the same value,
	// however, it is only guaranteed that one of them is set.
	reqBytes := request.GetCapacityRange().GetRequiredBytes()
	if reqBytes == 0 {
		reqBytes = capacityRange.GetLimitBytes()
	}
	reqCapacity := convertBytesToStorageUnit(capacityRange.GetRequiredBytes())
	createReq := getCreateDiskRequest(request.GetName(), reqCapacity, c.driver.GetNodeLocation(), capabilities)

	foundDisk, findErr := findDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetName())
	if findErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to validate disk if disk already exists: %w", findErr)
	}
	var disk *crusoeapi.DiskV1Alpha5
	// If disk already exists, make sure that it lines up with what we want
	if foundDisk != nil {
		verifyErr := verifyExistingDisk(foundDisk, createReq)
		if verifyErr != nil {
			return nil, status.Errorf(codes.AlreadyExists, "failed to validate disk if disk already exists: %w", verifyErr)
		}
		disk = foundDisk
	} else {
		// Create the disk if it does not already exist
		createdDisk, createErr := createDisk(ctx, c.apiClient, c.driver.GetNodeProject(), *createReq)
		if createErr != nil {
			return nil, createErr
		}
		disk = createdDisk
	}

	volume, parseErr := getVolumeFromDisk(disk)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to convert crusoe disk to csi volume: %w", parseErr)
	}

	return &csi.CreateVolumeResponse{
		Volume: volume,
	}, nil
}

func (c *ControllerServer) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	capacityRange := request.GetCapacityRange()
	// Note: both RequiredBytes and LimitBytes SHOULD be set to the same value,
	// however, it is only guaranteed that one of them is set.
	reqCapacity := parseCapacity(capacityRange)
	patchReq := crusoeapi.DisksPatchRequest{
		Size: reqCapacity,
	}

	volumeID := request.GetVolumeId()

	updatedDisk, updateErr := updateDisk(ctx, c.apiClient, c.driver.GetNodeProject(), volumeID, patchReq)
	if updateErr != nil {
		return nil, updateErr
	}

	newBytes, err := convertStorageUnitToBytes(updatedDisk.Size)
	if err != nil {
		return nil, err
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         newBytes,
		NodeExpansionRequired: false,
	}, nil
}

func (c *ControllerServer) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	err := deleteDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete disk: %w", err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c *ControllerServer) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ControllerModifyVolume(ctx context.Context, request *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	nodeCapabilities := make([]*csi.ControllerServiceCapability, 0, len(ControllerServerCapabilities))

	for _, capability := range ControllerServerCapabilities {
		nodeCapabilities = append(nodeCapabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: nodeCapabilities,
	}, nil
}
