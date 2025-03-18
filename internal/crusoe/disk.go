package crusoe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

	ErrInstanceNotFound = errors.New("instance not found")
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
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx, projectID)
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", common.UnpackSwaggerErr(listErr))
	}

	// indexing is used to avoid a copy
	for i := range disks.Items {
		currDisk := disks.Items[i]
		if currDisk.Name == name {
			return &currDisk, nil
		}
	}

	return nil, ErrDiskNotFound
}

func FindDiskByIDFallible(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	projectID string,
	diskID string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx, projectID)
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", common.UnpackSwaggerErr(listErr))
	}

	// indexing is used to avoid a copy
	for i := range disks.Items {
		currDisk := disks.Items[i]
		if currDisk.Id == diskID {
			return &currDisk, nil
		}
	}

	return nil, ErrDiskNotFound
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
		return err
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

func CheckDiskAttached(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	diskID,
	instanceID,
	projectID string,
) (bool, error) {
	instance, resp, err := crusoeClient.VMsApi.GetInstance(ctx, projectID, instanceID)

	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return false, ErrInstanceNotFound
	}

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
