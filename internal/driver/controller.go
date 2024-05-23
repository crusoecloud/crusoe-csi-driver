package driver

import (
	"context"
	"errors"
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

var errorRPCUnimplemented = errors.New("this RPC is currently not implemented")

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
			return nil, status.Errorf(codes.Internal, "failed to create disk: %w", createErr)
		}
		disk = createdDisk
	}

	volume, parseErr := getVolumeFromDisk(disk)
	if parseErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert crusoe disk to csi volume: %w", parseErr)
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
	diskID := request.GetVolumeId()
	instanceID := getInstanceIDFromNodeID(request.GetNodeId())
	attachmentMode, err := getAttachmentTypeFromVolumeCapability(request.GetVolumeCapability())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "received unexpected capability: %w", err)

	}
	attachment := crusoeapi.DiskAttachment{
		AttachmentType: attachmentMode,
		DiskId:         diskID,
		Mode:           attachmentMode,
	}

	attachReq := crusoeapi.InstancesAttachDiskPostRequestV1Alpha5{
		AttachDisks: []crusoeapi.DiskAttachment{attachment},
	}

	attachErr := attachDisk(ctx, c.apiClient, c.driver.GetNodeProject(), instanceID, attachReq)
	if attachErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to attach disk to vm: %w", attachErr)
	}

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: nil,
	}, nil
}

func (c *ControllerServer) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	diskID := request.GetVolumeId()
	instanceID := getInstanceIDFromNodeID(request.GetNodeId())

	detachReq := crusoeapi.InstancesDetachDiskPostRequest{
		DetachDisks: []string{diskID},
	}

	detachErr := detachDisk(ctx, c.apiClient, c.driver.GetNodeProject(), instanceID, detachReq)
	if detachErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to detach disk from vm: %w", detachErr)
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (c *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	capabilities := request.GetVolumeCapabilities()
	if capErr := validateVolumeCapabilities(capabilities); capErr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume capabilities: %w", capErr)
	}

	disk, getErr := getDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetVolumeId())
	if getErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get existing disk %w", getErr)
	}

	desiredType := getDiskTypFromVolumeType(capabilities)
	if desiredType != disk.Type_ {
		return &csi.ValidateVolumeCapabilitiesResponse{
			Message: fmt.Sprintf("disk does not satisfied the required capability"),
		}, nil
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      request.GetVolumeContext(),
			VolumeCapabilities: request.GetVolumeCapabilities(),
			Parameters:         request.GetParameters(),
		},
	}, nil
}

func (c *ControllerServer) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) ControllerModifyVolume(ctx context.Context, request *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errorRPCUnimplemented.Error())
}

func (c *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	controllerCapabilities := make([]*csi.ControllerServiceCapability, 0, len(ControllerServerCapabilities))

	for _, capability := range ControllerServerCapabilities {
		controllerCapabilities = append(controllerCapabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: controllerCapabilities,
	}, nil
}
