package driver

import (
	"context"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"strconv"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

const (
	BytesInGiB             = 1024 * 1024 * 1024
	BytesInTiB             = 1024 * 1024 * 1024 * 1024
	blockVolumeDiskType    = "persistent-ssd"
	mountVolumeDiskType    = "shared-volume"
	dataDiskAttachmentType = "data"
	readOnlyDiskMode       = "read-only"
	readWriteDiskMode      = "read-write"
	BlockSizeParam         = "csi.crusoe.ai/block-size"
)

var (
	errUnsupportedVolumeAccessMode = errors.New("unsupported access mode for volume")

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

	//nolint:gochecknoglobals // use this map to determine what capabilities are supported
	supportedBlockVolumeAccessMode = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:        {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:   {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:    {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER:  {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER: {},
	}
	//nolint:gochecknoglobals // use this map to determine what capabilities are supported
	supportedMountVolumeAccessMode = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:  {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER: {},
	}
)

func createDisk(ctx context.Context, apiClient *crusoeapi.APIClient,
	projectID string, createReq *crusoeapi.DisksPostRequestV1Alpha5,
) (*crusoeapi.DiskV1Alpha5, error) {
	dataResp, httpResp, err := apiClient.DisksApi.CreateDisk(ctx, *createReq, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to start a create disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	disk, _, err := awaitOperationAndResolve[crusoeapi.DiskV1Alpha5](ctx, dataResp.Operation, projectID,
		apiClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk: %w", err)
	}

	return disk, nil
}

func attachDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, vmID string,
	attachReq *crusoeapi.InstancesAttachDiskPostRequestV1Alpha5,
) error {
	dataResp, httpResp, err := apiClient.VMsApi.UpdateInstanceAttachDisks(ctx, *attachReq, projectID, vmID)
	if err != nil {
		return fmt.Errorf("failed to start an attach disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	_, err = awaitOperation(ctx, dataResp.Operation, projectID,
		apiClient.VMOperationsApi.GetComputeVMsInstancesOperation)
	if err != nil {
		return fmt.Errorf("failed to attach disk: %w", err)
	}

	return nil
}

func detachDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, vmID string,
	detachReq *crusoeapi.InstancesDetachDiskPostRequest,
) error {
	dataResp, httpResp, err := apiClient.VMsApi.UpdateInstanceDetachDisks(ctx, *detachReq, projectID, vmID)
	if err != nil {
		return fmt.Errorf("failed to start a detach disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	_, err = awaitOperation(ctx, dataResp.Operation, projectID,
		apiClient.VMOperationsApi.GetComputeVMsInstancesOperation)
	if err != nil {
		return fmt.Errorf("failed to detach disk: %w", err)
	}

	return nil
}

func updateDisk(ctx context.Context, apiClient *crusoeapi.APIClient,
	projectID, diskID string, updateReq *crusoeapi.DisksPatchRequest,
) (*crusoeapi.DiskV1Alpha5, error) {
	dataResp, httpResp, err := apiClient.DisksApi.ResizeDisk(ctx, *updateReq, projectID, diskID)
	if err != nil {
		return nil, fmt.Errorf("failed to start a create disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	disk, _, err := awaitOperationAndResolve[crusoeapi.DiskV1Alpha5](ctx, dataResp.Operation, projectID,
		apiClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk: %w", err)
	}

	return disk, nil
}

func deleteDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, diskID string) error {
	dataResp, httpResp, err := apiClient.DisksApi.DeleteDisk(ctx, projectID, diskID)
	if err != nil {
		return fmt.Errorf("failed to start a delete disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	_, err = awaitOperation(ctx, dataResp.Operation, projectID, apiClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		return fmt.Errorf("failed to delete disk: %w", err)
	}

	return nil
}

func findDisk(ctx context.Context, apiClient *crusoeapi.APIClient,
	projectID, name string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disks, httpResp, listErr := apiClient.DisksApi.ListDisks(ctx, projectID)
	if listErr != nil {
		return nil, fmt.Errorf("error checking if volume exists: %w", listErr)
	}
	defer httpResp.Body.Close()
	var foundDisk *crusoeapi.DiskV1Alpha5
	for i := range disks.Items {
		currDisk := disks.Items[i]
		if currDisk.Name == name {
			foundDisk = &currDisk

			break
		}
	}

	return foundDisk, nil
}

func getDisk(ctx context.Context, apiClient *crusoeapi.APIClient,
	projectID, diskID string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disk, httpResp, listErr := apiClient.DisksApi.GetDisk(ctx, projectID, diskID)
	if listErr != nil {
		return nil, fmt.Errorf("error checking if volume exists: %w", listErr)
	}
	defer httpResp.Body.Close()

	return &disk, nil
}

func convertStorageUnitToBytes(storageStr string) (int64, error) {
	valueStr := storageStr[:len(storageStr)-3]
	unit := storageStr[len(storageStr)-3:]

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric value: %w", err)
	}

	var totalBytes int64
	switch unit {
	case "GiB":
		totalBytes = int64(value * BytesInGiB)
	case "TiB":
		totalBytes = int64(value * BytesInTiB)
	default:
		//nolint:goerr113 // use dynamic errors for more informative error handling
		return 0, fmt.Errorf("received invalid unit: %s", unit)
	}

	return totalBytes, nil
}

// convertBytesToStorageUnit converts bytes to a specified unit (GiB or TiB) and returns the result as a string.
func convertBytesToStorageUnit(bytes int64) string {
	var size int64
	var unit string

	if unitsTiB := bytes / BytesInTiB; unitsTiB > 1 {
		size = unitsTiB
		unit = "TiB"
	} else {
		size = bytes / BytesInGiB
		unit = "GiB"
	}

	return fmt.Sprintf("%d%s", size, unit)
}

func getVolumeFromDisk(disk *crusoeapi.DiskV1Alpha5) (*csi.Volume, error) {
	volBytes, err := convertStorageUnitToBytes(disk.Size)
	if err != nil {
		return nil, fmt.Errorf("failed to parse disk storage: %w", err)
	}

	// The disk is only attachable to instances in its location
	accessibleTopology := &csi.Topology{
		Segments: map[string]string{
			TopologyLocationKey: disk.Location,
		},
	}

	volumeContext := map[string]string{
		VolumeContextDiskTypeKey:         disk.Type_,
		VolumeContextDiskSerialNumberKey: disk.SerialNumber,
	}

	return &csi.Volume{
		CapacityBytes:      volBytes,
		VolumeId:           disk.Id,
		VolumeContext:      volumeContext,
		ContentSource:      nil,
		AccessibleTopology: []*csi.Topology{accessibleTopology},
	}, nil
}

//nolint:cyclop // complexity comes from argument validation
func validateVolumeCapabilities(capabilities []*csi.VolumeCapability) error {
	for _, capability := range capabilities {
		if capability.GetBlock() != nil && capability.GetMount() != nil {
			return errBlockAndMountSpecified
		}
		if capability.GetBlock() == nil && capability.GetMount() == nil {
			return errNoCapabilitiesSpecified
		}

		accessMode := capability.GetAccessMode().GetMode()
		if capability.GetBlock() != nil {
			if _, ok := supportedBlockVolumeAccessMode[accessMode]; !ok {
				return fmt.Errorf("%w: %s", errUnsupportedBlockAccessMode, accessMode)
			}
		}
		if capability.GetMount() != nil {
			_, mountOk := supportedMountVolumeAccessMode[accessMode]
			_, blockOk := supportedBlockVolumeAccessMode[accessMode]

			// mount volumes can do everything block can too
			if !blockOk && !mountOk {
				return fmt.Errorf("%w: %s", errUnsupportedMountAccessMode, accessMode)
			}
		}
	}

	return nil
}

func getDiskTypeFromVolumeType(capabilities []*csi.VolumeCapability) string {
	for _, capability := range capabilities {
		accessMode := capability.GetAccessMode().GetMode()
		if _, mountOk := supportedMountVolumeAccessMode[accessMode]; mountOk {
			return mountVolumeDiskType
		} else if _, blockOk := supportedBlockVolumeAccessMode[accessMode]; blockOk {
			return blockVolumeDiskType
		}
	}

	return ""
}

func parseAndValidateBlockSize(strBlockSize string) (int64, error) {
	parsedBlockSize, err := strconv.Atoi(strBlockSize)
	if err != nil {
		return 0, fmt.Errorf("invalid block size argument: %w", err)
	}
	if parsedBlockSize != 512 && parsedBlockSize != 4096 {
		return 0, errInvalidBlockSize
	}

	return int64(parsedBlockSize), nil
}

func getCreateDiskRequest(name, capacity, location string,
	capabilities []*csi.VolumeCapability, optionalParameters map[string]string,
) (*crusoeapi.DisksPostRequestV1Alpha5, error) {
	params := &crusoeapi.DisksPostRequestV1Alpha5{
		Name:     name,
		Size:     capacity,
		Location: location,
	}
	if blockSize, ok := optionalParameters[BlockSizeParam]; ok {
		parsedBlockSize, err := parseAndValidateBlockSize(blockSize)
		if err != nil {
			return nil, fmt.Errorf("failed to validate block size: %w", err)
		}
		params.BlockSize = parsedBlockSize
	}

	params.Type_ = getDiskTypeFromVolumeType(capabilities)

	return params, nil
}

func verifyExistingDisk(currentDisk *crusoeapi.DiskV1Alpha5, createReq *crusoeapi.DisksPostRequestV1Alpha5) error {
	if currentDisk.Size != createReq.Size {
		return errDiskDifferentSize
	}
	if currentDisk.Name != createReq.Name {
		return errDiskDifferentName
	}
	if currentDisk.Location != createReq.Location {
		return errDiskDifferentLocation
	}
	if currentDisk.BlockSize != createReq.BlockSize {
		return errDiskDifferentBlockSize
	}
	if currentDisk.Type_ != createReq.Type_ {
		return errDiskDifferentType
	}

	return nil
}

func parseCapacity(capacityRange *csi.CapacityRange) string {
	// Note: both RequiredBytes and LimitBytes SHOULD be set to the same value,
	// however, it is only guaranteed that one of them is set.
	reqBytes := capacityRange.GetRequiredBytes()
	if reqBytes == 0 {
		reqBytes = capacityRange.GetLimitBytes()
	}
	reqCapacity := convertBytesToStorageUnit(reqBytes)

	return reqCapacity
}

func getAttachmentTypeFromVolumeCapability(capability *csi.VolumeCapability) (string, error) {
	accessMode := capability.GetAccessMode().GetMode()
	switch accessMode {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:
		return readWriteDiskMode, nil
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return readOnlyDiskMode, nil
	case csi.VolumeCapability_AccessMode_UNKNOWN:
		return "", errUnexpectedVolumeCapability
	}

	return "", fmt.Errorf("%w: %s", errUnsupportedVolumeAccessMode, accessMode.String())
}
