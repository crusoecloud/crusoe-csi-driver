package crusoe

import (
	"context"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"k8s.io/klog/v2"
)

//type Server interface {
//	GetCrusoeClient() *crusoeapi.APIClient
//	GetHostInstance() *crusoeapi.InstanceV1Alpha5
//	CreateVolume(context.Context, *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error)
//	DeleteVolume(context.Context, *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error)
//	ControllerPublishVolume(context.Context, *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error)
//	ControllerUnpublishVolume(context.Context, *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error)
//	ValidateVolumeCapabilities(context.Context, *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error)
//	ListVolumes(context.Context, *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error)
//	GetCapacity(context.Context, *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error)
//	ControllerGetCapabilities(context.Context, *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error)
//	CreateSnapshot(context.Context, *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error)
//	DeleteSnapshot(context.Context, *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error)
//	ListSnapshots(context.Context, *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error)
//	ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error)
//	ControllerGetVolume(context.Context, *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error)
//	ControllerModifyVolume(context.Context, *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error)
//	mustEmbedUnimplementedControllerServer()
//}

type DefaultController struct {
	csi.UnimplementedControllerServer
	CrusoeClient *crusoeapi.APIClient
	HostInstance *crusoeapi.InstanceV1Alpha5
	DiskType     DiskType
}

func NewDefaultService() DefaultController {
	return DefaultController{}
}

func (d *DefaultController) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.Infof("Received request to create volume: %+v", request)

	// Check if a volume already exists with the provided name
	existingDisk, err := findDiskByNameFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, request.GetName())

	if err != nil {
		if !errors.Is(err, errDiskNotFound) {
			return nil, err
		}
	}

	diskRequest, err := getCreateDiskRequest(request, d.HostInstance.Location, d.DiskType)

	if err != nil {
		return nil, err
	}

	var disk *crusoeapi.DiskV1Alpha5

	if existingDisk != nil {
		// Check if existing existingDisk matches what we want
		if diskMatchErr := checkDiskMatchesRequest(existingDisk, request, d.HostInstance.Location, d.DiskType); diskMatchErr != nil {
			// Disk does not match
			// To be safe, do not modify or delete existing disk and return error
			return nil, fmt.Errorf("disk %s already exists but does not match request: %w", request.GetName(), diskMatchErr)
		}

		disk = existingDisk
	} else {

		// Create the disk
		op, _, createErr := d.CrusoeClient.DisksApi.CreateDisk(ctx, *diskRequest, d.HostInstance.ProjectId)

		if createErr != nil {
			return nil, createErr
		}

		// Get the created disk
		newDisk, _, getResultErr := getAsyncOperationResult[crusoeapi.DiskV1Alpha5](ctx, op.Operation, d.HostInstance.ProjectId, d.CrusoeClient.DiskOperationsApi.GetStorageDisksOperation)

		if getResultErr != nil {
			return nil, getResultErr
		}

		disk = newDisk
	}

	volume, convertDiskErr := getVolumeFromDisk(disk)

	if convertDiskErr != nil {
		return nil, convertDiskErr
	}

	return &csi.CreateVolumeResponse{
		Volume: volume,
	}, nil
}

func (d *DefaultController) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	op, _, deleteErr := d.CrusoeClient.DisksApi.DeleteDisk(ctx, d.HostInstance.ProjectId, request.GetVolumeId())

	if deleteErr != nil {
		return nil, deleteErr
	}

	_, err := awaitOperation(ctx, op.Operation, d.HostInstance.ProjectId, d.CrusoeClient.DiskOperationsApi.GetStorageDisksOperation)

	if err != nil {
		return nil, err
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (d *DefaultController) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ControllerGetCapabilities(ctx context.Context, request *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) ControllerModifyVolume(ctx context.Context, request *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (d *DefaultController) mustEmbedUnimplementedControllerServer() {
	//TODO implement me
	panic("implement me")
}
