package crusoe

import (
	"context"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/client-go/swagger/v1alpha5"
	"math"
	"strconv"
	"strings"
)

type DiskType string

const (
	DiskTypeSSD DiskType = "persistent-ssd"
	DiskTypeFS  DiskType = "shared-filesystem"
)
const bytesInGiB = 1024 * 1024 * 1024
const blockSize = 4096

var (
	errUnsupportedVolumeAccessMode = errors.New("unsupported access mode for volume")

	errDiskNotFound               = errors.New("disk not found")
	errUnexpectedVolumeCapability = errors.New("unknown volume capability")
	errDiskDifferentSize          = errors.New("disk has different size")
	errDiskDifferentName          = errors.New("disk has different name")
	errDiskDifferentLocation      = errors.New("disk has different location")
	errDiskDifferentBlockSize     = errors.New("disk has different block size")
	errDiskDifferentType          = errors.New("disk has different type")
	errUnsupportedMountAccessMode = errors.New("unsupported access mode for mount volume")
	errUnsupportedBlockAccessMode = errors.New("unsupported access mode for block volume")
	errNoCapabilitiesSpecified    = errors.New("neither block nor mount capability specified")
	errBlockAndMountSpecified     = errors.New("both block and mount capabilities specified")
	errInvalidBlockSize           = errors.New("invalid block size specified: must be 512 or 4096")
)

func requestSizeToGiB(request *csi.CreateVolumeRequest) (int, error) {
	var requestSizeBytes int64

	if request.CapacityRange.RequiredBytes != 0 {
		requestSizeBytes = request.CapacityRange.GetRequiredBytes()
	} else if request.CapacityRange.LimitBytes != 0 {
		requestSizeBytes = request.CapacityRange.GetLimitBytes()
	} else {
		return 0, errors.New("no size specified")
	}

	requestSizeGiB := int(math.Ceil(float64(requestSizeBytes) / float64(bytesInGiB)))

	return requestSizeGiB, nil
}

func normalizeDiskSizeToGiB(disk *swagger.DiskV1Alpha5) (int, error) {
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
		return sizeTiB * 1024, nil
	} else {
		return 0, fmt.Errorf("unknown disk size suffix: %s", disk.Size)
	}
}

func findDiskByNameFallible(ctx context.Context,
	crusoeClient *swagger.APIClient,
	projectID string,
	name string,
) (*swagger.DiskV1Alpha5, error) {
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx, projectID)
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", listErr)
	}

	for i := range disks.Items {
		currDisk := disks.Items[i]
		if currDisk.Name == name {
			return &currDisk, nil
		}
	}

	return nil, errDiskNotFound
}

func getCreateDiskRequest(request *csi.CreateVolumeRequest, location string, diskType DiskType) (*swagger.DisksPostRequestV1Alpha5, error) {
	requestSizeGiB, err := requestSizeToGiB(request)
	if err != nil {
		return nil, err
	}

	return &swagger.DisksPostRequestV1Alpha5{
		BlockSize: blockSize, // TODO: Support different block sizes, ignore for shared disk
		Location:  location,
		Name:      request.GetName(),
		Size:      fmt.Sprintf("%dGiB", requestSizeGiB),
		Type_:     string(diskType),
	}, nil
}

func checkDiskMatchesRequest(disk *swagger.DiskV1Alpha5, request *csi.CreateVolumeRequest, expectedLocation string, expectedType DiskType) error {
	if disk.Name != request.GetName() {
		// This should never happen because we fetch the disk by name
		return errDiskDifferentName
	}

	if disk.BlockSize != blockSize { // TODO: Support different block sizes, ignore for shared disk
		return errDiskDifferentBlockSize
	}

	diskSizeGiB, err := normalizeDiskSizeToGiB(disk)
	if err != nil {
		return err
	}
	requestSizeGiB, err := requestSizeToGiB(request)
	if err != nil {
		return err
	}

	if diskSizeGiB != requestSizeGiB {
		return errDiskDifferentSize
	}

	if disk.Location != expectedLocation {
		return errDiskDifferentLocation
	}

	if disk.Type_ != string(expectedType) {
		return errDiskDifferentType
	}

	return nil
}

func getVolumeFromDisk(disk *swagger.DiskV1Alpha5) (*csi.Volume, error) {
	diskSizeGiB, err := normalizeDiskSizeToGiB(disk)
	if err != nil {
		return nil, fmt.Errorf("failed to parse disk size: %w", err)
	}
	return &csi.Volume{
		CapacityBytes: int64(bytesInGiB * diskSizeGiB),
		VolumeId:      disk.Id,
		// TODO: Support topology constraints
	}, nil
}

//func CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest, crusoeClient *crusoeapi.APIClient, hostInstance *swagger.InstanceV1Alpha5, diskType DiskType) (*csi.CreateVolumeResponse, error) {
//	klog.Infof("Received request to create volume: %+v", request)
//
//	// Check if a volume already exists with the provided name
//	disk, err := findDiskByNameFallible(ctx, crusoeClient, hostInstance.ProjectId, request.GetName())
//
//	if err != nil {
//		return nil, err
//	}
//
//	diskRequest, err := getCreateDiskRequest(request, hostInstance.Location, diskType)
//
//	if err != nil {
//		return nil, err
//	}
//
//	if disk != nil {
//		// TODO: Handle disk already existing
//		//return nil, status.Errorf(codes.AlreadyExists, "volume with name %s already exists", request.GetName())
//		return nil, nil
//	}
//
//	// Create the disk
//	_, _, err = crusoeClient.DisksApi.CreateDisk(ctx, *diskRequest, hostInstance.ProjectId)
//
//	// TODO: await operation
//
//	if err != nil {
//		return nil, err
//	}
//
//	panic("todo")
//}
