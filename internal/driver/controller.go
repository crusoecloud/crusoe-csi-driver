package driver

import (
	"context"
	"errors"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

//nolint:gochecknoglobals // we will use this slice to determine what the controller service supports
var controllerServerCapabilities = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
	csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
}

const diskUnsatisfactoryMsg = "disk does not satisfied the required capability"

var (
	errRPCUnimplemented            = errors.New("this RPC is currently not implemented")
	errExpandVolumeWithSmallerSize = errors.New("disk currently has larger size than expand volume request")
)

type ControllerServer struct {
	apiClient *crusoeapi.APIClient
	driver    *Config
}

func NewControllerServer() *ControllerServer {
	return &ControllerServer{}
}

func (c *ControllerServer) Init(apiClient *crusoeapi.APIClient, driver *Config, _ []Service) error {
	c.driver = driver
	c.apiClient = apiClient

	return nil
}

func (c *ControllerServer) RegisterServer(srv *grpc.Server) error {
	csi.RegisterControllerServer(srv, c)

	return nil
}

func (c *ControllerServer) CreateVolume(ctx context.Context,
	request *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {
	klog.Infof("Received request to create volume: %+v", request)

	capabilities := request.GetVolumeCapabilities()
	if capErr := validateVolumeCapabilities(capabilities); capErr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume capabilities: %s", capErr.Error())
	}

	reqCapacity := parseCapacity(request.GetCapacityRange())
	createReq, err := getCreateDiskRequest(request.GetName(), reqCapacity, c.driver.GetNodeLocation(),
		capabilities, request.GetParameters())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid arguments to create volume: %s", err.Error())
	}

	// We will check if a disk already exists with the provided name
	foundDisk, findErr := findDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetName())
	if findErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"failed to validate disk if disk already exists: %s", findErr.Error())
	}
	var disk *crusoeapi.DiskV1Alpha5
	// If a disk already exists, make sure that it lines up with what we want
	if foundDisk != nil {
		verifyErr := verifyExistingDisk(foundDisk, createReq)
		if verifyErr != nil {
			return nil, status.Errorf(codes.AlreadyExists,
				"failed to validate disk if disk already exists: %s", verifyErr.Error())
		}
		disk = foundDisk
	} else {
		// Create the disk if it does not already exist
		createdDisk, createErr := createDisk(ctx, c.apiClient, c.driver.GetNodeProject(), createReq)
		if createErr != nil {
			return nil, status.Errorf(codes.Internal, "failed to create disk: %s", createErr.Error())
		}
		disk = createdDisk
	}

	volume, parseErr := getVolumeFromDisk(disk)
	if parseErr != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to convert crusoe disk to csi volume: %s", parseErr.Error())
	}

	klog.Infof("Successfully created volume with name: %s and capacity: %s", request.GetName(), reqCapacity)

	return &csi.CreateVolumeResponse{
		Volume: volume,
	}, nil
}

func (c *ControllerServer) ControllerExpandVolume(ctx context.Context,
	request *csi.ControllerExpandVolumeRequest,
) (*csi.ControllerExpandVolumeResponse, error) {
	klog.Infof("Received request to expand volume: %+v", request)
	capacityRange := request.GetCapacityRange()

	reqCapacity := parseCapacity(capacityRange)

	disk, getErr := getDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetVolumeId())
	if getErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get existing disk: %s",
			getErr.Error())
	}

	if disk.Size > reqCapacity {
		return nil, status.Errorf(codes.InvalidArgument, "invalid expand volume request: %s",
			errExpandVolumeWithSmallerSize.Error())
	}

	patchReq := &crusoeapi.DisksPatchRequest{
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

	klog.Infof("Successfully expanded volume with ID: %s", request.GetVolumeId())

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         newBytes,
		NodeExpansionRequired: false,
	}, nil
}

func (c *ControllerServer) DeleteVolume(ctx context.Context,
	request *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {
	err := deleteDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete disk: %s", err.Error())
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c *ControllerServer) ControllerPublishVolume(ctx context.Context,
	request *csi.ControllerPublishVolumeRequest,
) (*csi.ControllerPublishVolumeResponse, error) {
	klog.Infof("Received request to publish volume: %+v", request)
	diskID := request.GetVolumeId()
	instanceID := request.GetNodeId()
	attachmentMode, err := getAttachmentTypeFromVolumeCapability(request.GetVolumeCapability())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "received unexpected capability: %s", err.Error())
	}

	attachment := crusoeapi.DiskAttachment{
		AttachmentType: dataDiskAttachmentType,
		DiskId:         diskID,
		Mode:           attachmentMode,
	}

	attachReq := &crusoeapi.InstancesAttachDiskPostRequestV1Alpha5{
		AttachDisks: []crusoeapi.DiskAttachment{attachment},
	}

	attachErr := attachDisk(ctx, c.apiClient, c.driver.GetNodeProject(), instanceID, attachReq)
	if attachErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to attach disk to node: %s", attachErr.Error())
	}

	klog.Infof("Successfully published volume with ID: %s to node: %s",
		request.GetVolumeId(), request.GetNodeId())

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: nil,
	}, nil
}

func (c *ControllerServer) ControllerUnpublishVolume(ctx context.Context,
	request *csi.ControllerUnpublishVolumeRequest,
) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.Infof("Received request to unpublish volume: %+v", request)
	diskID := request.GetVolumeId()
	instanceID := request.GetNodeId()

	detachReq := &crusoeapi.InstancesDetachDiskPostRequest{
		DetachDisks: []string{diskID},
	}

	detachErr := detachDisk(ctx, c.apiClient, c.driver.GetNodeProject(), instanceID, detachReq)
	if detachErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to detach disk from vm: %s", detachErr.Error())
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (c *ControllerServer) ValidateVolumeCapabilities(ctx context.Context,
	request *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.Infof("Received request to validate volume capabilities: %+v", request)
	capabilities := request.GetVolumeCapabilities()
	if capErr := validateVolumeCapabilities(capabilities); capErr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume capabilities: %s", capErr.Error())
	}

	disk, getErr := getDisk(ctx, c.apiClient, c.driver.GetNodeProject(), request.GetVolumeId())
	if getErr != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get existing disk %s", getErr.Error())
	}

	desiredType := getDiskTypeFromVolumeType(capabilities)
	// as part of the CSI specification, if the set of capabilities is not supported, the Confirmed field of the
	// response should be empty – when Confirmed is empty, we can optionally include a message for K8s to report
	// why the capabilities are unsupported
	if desiredType != disk.Type_ {
		return &csi.ValidateVolumeCapabilitiesResponse{
			Message: diskUnsatisfactoryMsg,
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

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) ListVolumes(_ context.Context,
	_ *csi.ListVolumesRequest,
) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) ControllerGetVolume(_ context.Context,
	_ *csi.ControllerGetVolumeRequest,
) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) GetCapacity(_ context.Context,
	_ *csi.GetCapacityRequest,
) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) CreateSnapshot(_ context.Context,
	_ *csi.CreateSnapshotRequest,
) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) DeleteSnapshot(_ context.Context,
	_ *csi.DeleteSnapshotRequest,
) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) ListSnapshots(_ context.Context,
	_ *csi.ListSnapshotsRequest,
) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

//nolint:wrapcheck // we want to return gRPC Status errors
func (c *ControllerServer) ControllerModifyVolume(_ context.Context,
	_ *csi.ControllerModifyVolumeRequest,
) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, errRPCUnimplemented.Error())
}

func (c *ControllerServer) ControllerGetCapabilities(_ context.Context,
	_ *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {
	controllerCapabilities := make([]*csi.ControllerServiceCapability, 0, len(controllerServerCapabilities))

	for _, capability := range controllerServerCapabilities {
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
