package crusoe

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/antihax/optional"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
)

var (
	ErrUnknownDiskSizeSuffix = errors.New("unknown disk size suffix")

	ErrDiskNotFound           = errors.New("disk not found")
	ErrDiskDifferentSize      = errors.New("disk has different size")
	ErrDiskDifferentName      = errors.New("disk has different name")
	ErrDiskDifferentLocation  = errors.New("disk has different location")
	ErrDiskDifferentBlockSize = errors.New("disk has different block size")
	ErrDiskDifferentType      = errors.New("disk has different type")

	ErrInstanceNotFound  = errors.New("instance not found")
	ErrMultipleInstances = errors.New("multiple instances found")
)

func NormalizeDiskSizeToGiB(disk *crusoeapi.DiskV1Alpha5) (int, error) {
	if strings.HasSuffix(disk.Size, "GiB") {
		sizeGiB, err := strconv.Atoi(strings.TrimSuffix(disk.Size, "GiB"))
		if err != nil {
			return 0, fmt.Errorf("failed to parse disk size: %w", err)
		}

		return sizeGiB, nil
	} else if strings.HasSuffix(disk.Size, "TiB") {
		sizeTiB, err := strconv.Atoi(strings.TrimSuffix(disk.Size, "TiB"))
		if err != nil {
			return 0, fmt.Errorf("failed to parse disk size: %w", err)
		}

		return sizeTiB * common.NumGiBInTiB, nil
	}

	return 0, fmt.Errorf("%w: %s", ErrUnknownDiskSizeSuffix, disk.Size)
}

func FindDiskByNameFallible(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	projectID string,
	name string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx,
		projectID,
		&crusoeapi.DisksApiListDisksOpts{DiskNames: optional.NewInterface([]string{name})})
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", common.UnpackSwaggerErr(listErr))
	}

	if len(disks.Items) != 1 {
		return nil, fmt.Errorf("%w: found %d disks with name %s, expected 1", ErrDiskNotFound, len(disks.Items), name)
	}

	return &disks.Items[0], nil
}

func FindDiskByIDFallible(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	projectID string,
	diskID string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx,
		projectID,
		&crusoeapi.DisksApiListDisksOpts{DiskIds: optional.NewInterface([]string{diskID})})
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", common.UnpackSwaggerErr(listErr))
	}

	if len(disks.Items) != 1 {
		return nil, fmt.Errorf("%w: found %d disks with id %s, expected 1", ErrDiskNotFound, len(disks.Items), diskID)
	}

	return &disks.Items[0], nil
}

func GetCreateDiskRequest(request *csi.CreateVolumeRequest,
	location string,
	diskType common.DiskType,
) (*crusoeapi.DisksPostRequestV1Alpha5, error) {
	requestSizeGiB, err := common.RequestSizeToGiB(request.GetCapacityRange())
	if err != nil {
		return nil, fmt.Errorf("failed to parse request size: %w", err)
	}

	var blockSize int64

	if diskType == common.DiskTypeSSD {
		blockSize = common.BlockSizeSSD // TODO: Support different block sizes
	}

	return &crusoeapi.DisksPostRequestV1Alpha5{
		BlockSize: blockSize,
		Location:  location,
		Name:      request.GetName(),
		Size:      fmt.Sprintf("%dGiB", requestSizeGiB),
		Type_:     string(diskType),
	}, nil
}

func CheckDiskMatchesRequest(disk *crusoeapi.DiskV1Alpha5,
	request *csi.CreateVolumeRequest,
	expectedLocation string,
	expectedType common.DiskType,
) error {
	if disk.Name != request.GetName() {
		// This should never happen because we fetch the disk by name
		return ErrDiskDifferentName
	}

	// TODO: Support different block sizes
	if disk.Type_ == string(common.DiskTypeSSD) && disk.BlockSize != common.BlockSizeSSD {
		return ErrDiskDifferentBlockSize
	}

	diskSizeGiB, err := NormalizeDiskSizeToGiB(disk)
	if err != nil {
		return fmt.Errorf("failed to parse disk size: %w", err)
	}

	requestSizeGiB, err := common.RequestSizeToGiB(request.GetCapacityRange())
	if err != nil {
		return fmt.Errorf("failed to parse request size: %w", err)
	}

	if diskSizeGiB != requestSizeGiB {
		return ErrDiskDifferentSize
	}

	if disk.Location != expectedLocation {
		return ErrDiskDifferentLocation
	}

	if disk.Type_ != string(expectedType) {
		return ErrDiskDifferentType
	}

	return nil
}

func GetVolumeFromDisk(disk *crusoeapi.DiskV1Alpha5,

	pluginName,
	location string,
	diskType common.DiskType) (
	*csi.Volume,
	error,
) {
	diskSizeGiB, err := NormalizeDiskSizeToGiB(disk)
	if err != nil {
		return nil, fmt.Errorf("failed to parse disk size: %w", err)
	}

	segments := map[string]string{
		fmt.Sprintf("%s/location", pluginName): location,
	}

	if diskType == common.DiskTypeFS {
		segments[common.GetTopologyKey(pluginName, common.TopologySupportsSharedDisksKey)] = strconv.FormatBool(true)
	}

	return &csi.Volume{
		CapacityBytes: int64(common.NumBytesInGiB) * int64(diskSizeGiB),
		VolumeId:      disk.Id,
		VolumeContext: map[string]string{
			common.VolumeContextDiskSerialNumberKey: disk.SerialNumber,
			common.VolumeContextDiskNameKey:         disk.Name,
		},
		AccessibleTopology: []*csi.Topology{
			{
				Segments: segments,
			},
		},
	}, nil
}

func GetInstanceByID(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	instanceID,
	projectID string,
) (*crusoeapi.InstanceV1Alpha5, error) {
	listVMOpts := &crusoeapi.VMsApiListInstancesOpts{
		Ids: optional.NewString(instanceID),
	}
	instances, _, err := crusoeClient.VMsApi.ListInstances(ctx, projectID, listVMOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances.Items) == 0 {
		return nil, fmt.Errorf("%w: found %d instances with id %s, expected 1",
			ErrInstanceNotFound, len(instances.Items), instanceID)
	} else if len(instances.Items) > 1 {
		return nil, fmt.Errorf("%w: found %d instances with id %s, expected 1",
			ErrMultipleInstances, len(instances.Items), instanceID)
	}

	return &instances.Items[0], nil
}

func CheckDiskAttached(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	diskID,
	instanceID,
	projectID string,
) (bool, error) {
	// Use GetInstanceByID (ListInstances) instead of GetInstance because we can easily identify
	// when an instance is not found
	instance, err := GetInstanceByID(ctx, crusoeClient, instanceID, projectID)
	if err != nil {
		return false, fmt.Errorf("failed to get instance: %w", err)
	}

	for i := range instance.Disks {
		if instance.Disks[i].Id == diskID {
			return true, nil
		}
	}

	return false, nil
}
