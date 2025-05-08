package controller

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/klog/v2"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

type DefaultController struct {
	csi.UnimplementedControllerServer
	CrusoeClient  *crusoeapi.APIClient
	HostInstance  *crusoeapi.InstanceV1Alpha5
	DiskType      common.DiskType
	PluginName    string
	PluginVersion string
	Capabilities  []*csi.ControllerServiceCapability
}

//nolint:funlen,cyclop // function is already fairly clean
func (d *DefaultController) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse,
	error,
) {
	klog.Infof("Received request to create volume: %+v", request)

	err := validateDiskRequest(request, d.DiskType)
	if err != nil {
		return nil, err // validateDiskRequest returns only status.Errors so we can return the error directly
	}

	// Trim the PVC prefix from the request name
	// Crusoe Shared Disks have a limit of 36 characters on the name field
	request.Name = common.TrimPVCPrefix(request.GetName())
	if len(request.Name) > common.MaxDiskNameLength {
		request.Name = request.Name[:common.MaxDiskNameLength]
	}

	// Check if a volume already exists with the provided name
	existingDisk, err := crusoe.FindDiskByNameFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, request.GetName())
	if err != nil {
		if !errors.Is(err, crusoe.ErrDiskNotFound) {
			klog.Errorf("failed to check if disk exists: %s", err)

			return nil, status.Errorf(codes.Internal, "failed to check if disk exists: %s", err)
		}
	}

	diskLocation, requireSupportsFS := parseRequiredTopology(request, d.DiskType, d.PluginName, d.HostInstance)
	if d.DiskType == common.DiskTypeFS && !requireSupportsFS {
		klog.Errorf("shared disk requested but could not find topology constraint with %s and %s segments",
			common.GetTopologyKey(d.PluginName, common.TopologyLocationKey),
			common.GetTopologyKey(d.PluginName, common.TopologySupportsSharedDisksKey))

		return nil, status.Errorf(codes.ResourceExhausted,
			"shared disk requested but could not find topology constraint with %s and %s segments",
			common.GetTopologyKey(d.PluginName, common.TopologyLocationKey),
			common.GetTopologyKey(d.PluginName, common.TopologySupportsSharedDisksKey))
	}

	diskRequest, err := crusoe.GetCreateDiskRequest(request, diskLocation, d.DiskType)
	if err != nil {
		klog.Errorf("failed to get create disk request: %s", err)

		return nil, status.Errorf(codes.InvalidArgument, "failed to get create disk request: %s", err)
	}

	var disk *crusoeapi.DiskV1Alpha5

	if existingDisk != nil {
		// Check if existing existingDisk matches what we want
		if diskMatchErr := crusoe.CheckDiskMatchesRequest(existingDisk,
			request,
			d.HostInstance.Location,
			d.DiskType,
		); diskMatchErr != nil {
			// Disk does not match
			// To be safe, do not modify or delete existing disk and return error
			klog.Errorf("disk %s already exists but does not match request: %s",
				request.GetName(),
				diskMatchErr)

			return nil, status.Errorf(codes.AlreadyExists,
				"disk %s already exists but does not match request: %s",
				request.GetName(),
				diskMatchErr)
		}

		klog.Infof("Disk %s already exists, skipping creation", request.GetName())

		disk = existingDisk
	} else {
		// Create the disk
		op, _, createErr := d.CrusoeClient.DisksApi.CreateDisk(ctx, *diskRequest, d.HostInstance.ProjectId)
		if createErr != nil {
			klog.Errorf("failed to create disk: %s", common.UnpackSwaggerErr(createErr))

			return nil, status.Errorf(codes.Internal, "failed to create disk: %s", common.UnpackSwaggerErr(createErr))
		}

		// Get the created disk
		newDisk, _, getResultErr := common.GetAsyncOperationResult[crusoeapi.DiskV1Alpha5](ctx,
			op.Operation,
			d.HostInstance.ProjectId,
			d.CrusoeClient.DiskOperationsApi.GetStorageDisksOperation)
		if getResultErr != nil {
			klog.Errorf("failed to get result of disk creation: %s",
				common.UnpackSwaggerErr(getResultErr))

			return nil, status.Errorf(codes.Internal,
				"failed to get result of disk creation: %s",
				common.UnpackSwaggerErr(getResultErr))
		}

		disk = newDisk
	}

	volume, convertDiskErr := crusoe.GetVolumeFromDisk(disk, d.PluginName, diskLocation, d.DiskType)
	if convertDiskErr != nil {
		klog.Errorf("failed to convert crusoe disk to kubernetes volume: %s", convertDiskErr)

		return nil, status.Errorf(codes.Internal, "failed to convert crusoe disk to kubernetes volume: %s", convertDiskErr)
	}

	klog.Infof("Created volume: %+v", volume)

	return &csi.CreateVolumeResponse{
		Volume: volume,
	}, nil
}

func (d *DefaultController) DeleteVolume(ctx context.Context,
	request *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse,
	error,
) {
	klog.Infof("Received request to delete volume: %+v", request)

	// Check if the disk exists
	existingDisk, err := crusoe.FindDiskByIDFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, request.GetVolumeId())
	if errors.Is(err, crusoe.ErrDiskNotFound) {
		// Disk does not exist
		klog.Infof("Disk %s is already deleted, skipping deletion", request.GetVolumeId())

		return &csi.DeleteVolumeResponse{}, nil
	} else if err != nil {
		klog.Errorf("failed to check if disk exists: %s", err)

		return nil, status.Errorf(codes.FailedPrecondition, "failed to check if disk exists: %s", err)
	}

	if len(existingDisk.AttachedTo) > 0 {
		klog.Errorf("disk %s is still attached to instance(s): %v",
			request.GetVolumeId(),
			existingDisk.AttachedTo)

		return nil, status.Errorf(codes.FailedPrecondition,
			"disk %s is still attached to instance(s): %v",
			request.GetVolumeId(),
			existingDisk.AttachedTo)
	}

	op, _, err := d.CrusoeClient.DisksApi.DeleteDisk(ctx, d.HostInstance.ProjectId, request.GetVolumeId())
	if err != nil {
		klog.Errorf("failed to delete disk: %s", common.UnpackSwaggerErr(err))

		return nil, status.Errorf(codes.Internal, "failed to delete disk: %s", common.UnpackSwaggerErr(err))
	}

	_, awaitErr := common.AwaitOperation(ctx,
		op.Operation,
		d.HostInstance.ProjectId,
		d.CrusoeClient.DiskOperationsApi.GetStorageDisksOperation)
	if awaitErr != nil {
		klog.Errorf("failed to get result of disk deletion: %s",
			common.UnpackSwaggerErr(awaitErr))

		return nil, status.Errorf(codes.Internal,
			"failed to get result of disk deletion: %s",
			common.UnpackSwaggerErr(awaitErr))
	}

	klog.Infof("Deleted volume: %+v", request)

	return &csi.DeleteVolumeResponse{}, nil
}

func (d *DefaultController) ControllerPublishVolume(ctx context.Context,
	request *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to publish volume: %+v", request)

	// Check if the disk is already attached to the instance
	attached, err := crusoe.CheckDiskAttached(ctx,
		d.CrusoeClient,
		request.GetVolumeId(),
		request.GetNodeId(),
		d.HostInstance.ProjectId)
	if err != nil {
		klog.Errorf("failed to check if disk is attached to instance: %s", err)

		return nil, status.Errorf(codes.NotFound, "failed to check if disk is attached to instance: %s", err)
	}

	if attached {
		klog.Infof("Disk %s is already attached to instance %s, skipping publish", request.GetVolumeId(), request.GetNodeId())

		return &csi.ControllerPublishVolumeResponse{}, nil
	}

	accessMode := request.VolumeCapability.GetAccessMode().Mode
	mode := "read-write"
	if accessMode == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY ||
		accessMode == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {

		mode = "read-only"
	}

	op, _, err := d.CrusoeClient.VMsApi.UpdateInstanceAttachDisks(ctx, crusoeapi.InstancesAttachDiskPostRequestV1Alpha5{
		AttachDisks: []crusoeapi.DiskAttachment{
			{
				AttachmentType: "data",
				DiskId:         request.GetVolumeId(),
				Mode:           mode,
			},
		},
	}, d.HostInstance.ProjectId, request.GetNodeId())
	if err != nil {
		klog.Errorf("failed to attach disk: %s", err)

		return nil, status.Errorf(codes.Internal, "failed to attach disk: %s", err)
	}

	_, err = common.AwaitOperation(ctx,
		op.Operation,
		d.HostInstance.ProjectId,
		d.CrusoeClient.VMOperationsApi.GetComputeVMsInstancesOperation)
	if err != nil {
		klog.Errorf("failed to get result of disk attachment: %s", err)

		return nil, status.Errorf(codes.Internal, "failed to get result of disk attachment: %s", err)
	}

	klog.Infof("Published volume: %+v", request)

	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (d *DefaultController) ControllerUnpublishVolume(ctx context.Context,
	request *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse,
	error,
) {
	klog.Infof("Received request to unpublish volume: %+v", request)

	// Check if the disk is already detached from the instance
	attached, err := crusoe.CheckDiskAttached(ctx,
		d.CrusoeClient,
		request.GetVolumeId(),
		request.GetNodeId(),
		d.HostInstance.ProjectId)
	if err != nil {
		if errors.Is(err, crusoe.ErrInstanceNotFound) {
			// Instance does not exist
			klog.Infof("Instance %s is already deleted, skipping unpublish", request.GetNodeId())

			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		klog.Errorf("failed to check if disk is attached to instance: %s", err)

		return nil, status.Errorf(codes.NotFound, "failed to check if disk is attached to instance: %s", err)
	}

	if !attached {
		klog.Infof(
			"Disk %s is already detached from instance %s, skipping unpublish",
			request.GetVolumeId(),
			request.GetNodeId())

		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	op, _, err := d.CrusoeClient.VMsApi.UpdateInstanceDetachDisks(ctx, crusoeapi.InstancesDetachDiskPostRequest{
		DetachDisks: []string{
			request.GetVolumeId(),
		},
	}, d.HostInstance.ProjectId, request.GetNodeId())
	if err != nil {
		klog.Errorf("failed to detach disk: %s", err)

		return nil, status.Errorf(codes.Internal, "failed to detach disk: %s", err)
	}

	_, err = common.AwaitOperation(ctx,
		op.Operation,
		d.HostInstance.ProjectId,
		d.CrusoeClient.VMOperationsApi.GetComputeVMsInstancesOperation)
	if err != nil {
		klog.Errorf("failed to get result of disk detachment: %s", err)

		return nil, status.Errorf(codes.Internal, "failed to get result of disk detachment: %s", err)
	}

	klog.Infof("Unpublished volume: %+v", request)

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (d *DefaultController) ValidateVolumeCapabilities(_ context.Context,
	request *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse,
	error,
) {
	for _, capability := range request.GetVolumeCapabilities() {
		err := supportsCapability(capability, d.DiskType)
		if err != nil {
			//nolint:nilerr // An incompatible volume capability is not an error
			return &csi.ValidateVolumeCapabilitiesResponse{Message: err.Error()}, nil
		}
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      request.GetVolumeContext(),
			VolumeCapabilities: request.GetVolumeCapabilities(),
			Parameters:         request.GetParameters(),
			MutableParameters:  request.GetMutableParameters(),
		},
	}, nil
}

func (d *DefaultController) ListVolumes(_ context.Context, _ *csi.ListVolumesRequest) (
	*csi.ListVolumesResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: ListVolumes", common.ErrNotImplemented)
}

func (d *DefaultController) GetCapacity(_ context.Context, _ *csi.GetCapacityRequest) (
	*csi.GetCapacityResponse,
	error,
) {
	maxSize, minSize := getCapacity(d.DiskType)

	return &csi.GetCapacityResponse{
		// We don't know how much space is available, so return MaxInt64
		AvailableCapacity: math.MaxInt64,
		MaximumVolumeSize: wrapperspb.Int64(maxSize),
		MinimumVolumeSize: wrapperspb.Int64(minSize),
	}, nil
}

func (d *DefaultController) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse,
	error,
) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: d.Capabilities,
	}, nil
}

func (d *DefaultController) CreateSnapshot(_ context.Context, _ *csi.CreateSnapshotRequest) (
	*csi.CreateSnapshotResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: CreateSnapshot", common.ErrNotImplemented)
}

func (d *DefaultController) DeleteSnapshot(_ context.Context, _ *csi.DeleteSnapshotRequest) (
	*csi.DeleteSnapshotResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: DeleteSnapshot", common.ErrNotImplemented)
}

func (d *DefaultController) ListSnapshots(_ context.Context, _ *csi.ListSnapshotsRequest) (
	*csi.ListSnapshotsResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: ListSnapshots", common.ErrNotImplemented)
}

//nolint:cyclop,funlen // error handling
func (d *DefaultController) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (
	*csi.ControllerExpandVolumeResponse,
	error,
) {
	klog.Infof("Received request to expand volume: %+v", request)

	// Find the existing disk
	existingDisk, err := crusoe.FindDiskByIDFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, request.GetVolumeId())
	if err != nil {
		klog.Errorf("failed to find disk: %s", err)

		return nil, status.Errorf(codes.NotFound, "failed to find disk: %s", err)
	}

	// Only common.DiskTypeFS volumes can be expanded online
	if d.DiskType != common.DiskTypeFS && len(existingDisk.AttachedTo) != 0 {
		klog.Errorf("offline volume expansion failed: volume %s is attached to one or more nodes: %v",
			request.GetVolumeId(),
			existingDisk.AttachedTo)

		return nil, status.Errorf(
			codes.FailedPrecondition,
			"offline volume expansion failed: volume %s is attached to one or more nodes: %v",
			request.GetVolumeId(),
			existingDisk.AttachedTo)
	}

	existingSizeGiB, err := crusoe.NormalizeDiskSizeToGiB(existingDisk)
	if err != nil {
		klog.Errorf("failed to normalize disk size: %s", err)

		return nil, status.Errorf(codes.Internal, "failed to normalize disk size: %s", err)
	}

	// We compare the exact requestSizeBytes to min/maxSizeBytes to avoid strange behaviour when rounding
	// and to return an accurate error message
	requestSizeBytes, err := common.RequestSizeToBytes(request.GetCapacityRange())
	if err != nil {
		klog.Errorf("failed to get request size: %s", err)

		return nil, status.Errorf(codes.OutOfRange, "failed to get request size: %s", err)
	}
	requestSizeGiB, err := common.RequestSizeToGiB(request.GetCapacityRange())
	if err != nil {
		klog.Errorf("failed to get request size: %s", err)

		return nil, status.Errorf(codes.OutOfRange, "failed to get request size: %s", err)
	}

	maxSizeBytes, minSizeBytes := getCapacity(d.DiskType)

	if requestSizeBytes > maxSizeBytes {
		klog.Errorf("%s: maximum size: %d, requested size: %d",
			errDiskTooLarge, maxSizeBytes, requestSizeBytes)

		return nil, status.Errorf(codes.OutOfRange, "%s: maximum size: %d, requested size: %d",
			errDiskTooLarge, maxSizeBytes, requestSizeBytes)
	}

	if requestSizeBytes < minSizeBytes {
		klog.Errorf("%s: minimum size: %d, requested size: %d",
			errDiskTooSmall, minSizeBytes, requestSizeBytes)

		return nil, status.Errorf(codes.OutOfRange, "%s: minimum size: %d, requested size: %d",
			errDiskTooSmall, minSizeBytes, requestSizeBytes)
	}

	// requestSizeGiB is the actual size that is sent to the Crusoe API in the resize request
	existingSizeBytes := int64(existingSizeGiB) * common.NumBytesInGiB
	if existingSizeBytes >= requestSizeBytes {
		klog.Infof("Disk %s is already at or above the requested size %d GiB, skipping resize",
			request.GetVolumeId(),
			request.GetCapacityRange().GetRequiredBytes()/common.NumBytesInGiB)

		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         existingSizeBytes,
			NodeExpansionRequired: false,
		}, nil
	}

	op, _, err := d.CrusoeClient.DisksApi.ResizeDisk(ctx, crusoeapi.DisksPatchRequest{
		Size: fmt.Sprintf("%dGiB", requestSizeGiB),
	}, d.HostInstance.ProjectId, request.GetVolumeId())
	if err != nil {
		klog.Errorf("failed to resize disk: %s", common.UnpackSwaggerErr(err))

		return nil, status.Errorf(codes.Internal, "failed to resize disk: %s", common.UnpackSwaggerErr(err))
	}

	_, err = common.AwaitOperation(ctx,
		op.Operation,
		d.HostInstance.ProjectId,
		d.CrusoeClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		klog.Errorf("failed to get result of disk resize: %s",
			common.UnpackSwaggerErr(err))

		return nil, status.Errorf(codes.Internal,
			"failed to get result of disk resize: %s", common.UnpackSwaggerErr(err))
	}

	klog.Infof("Resized volume %s to %d GiB", request.GetVolumeId(), requestSizeGiB)

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         int64(requestSizeGiB) * common.NumBytesInGiB,
		NodeExpansionRequired: false,
	}, nil
}

func (d *DefaultController) ControllerGetVolume(_ context.Context, _ *csi.ControllerGetVolumeRequest) (
	*csi.ControllerGetVolumeResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: ControllerGetVolume", common.ErrNotImplemented)
}

func (d *DefaultController) ControllerModifyVolume(_ context.Context, _ *csi.ControllerModifyVolumeRequest) (
	*csi.ControllerModifyVolumeResponse,
	error,
) {
	return nil, status.Errorf(codes.Unimplemented, "%s: ControllerModifyVolume", common.ErrNotImplemented)
}
