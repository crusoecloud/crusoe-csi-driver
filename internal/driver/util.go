package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	swagger "github.com/crusoecloud/client-go/swagger/v1alpha4"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	pollInterval        = 2 * time.Second
	BytesInGiB          = 1024 * 1024 * 1024
	BytesInTiB          = 1024 * 1024 * 1024 * 1024
	blockVolumeDiskType = "persistent-ssd"
	mountVolumeDiskType = "shared-volume"
	readOnlyDiskMode    = "read-only"
	readWriteDiskMode   = "read-write"
	identifierDelimiter = "|"
)

// apiError models the error format returned by the Crusoe API go client.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type opStatus string

type opResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var (
	OpSucceeded  opStatus = "SUCCEEDED"
	OpInProgress opStatus = "IN_PROGRESS"
	OpFailed     opStatus = "FAILED"

	errUnableToGetOpRes            = errors.New("failed to get result of operation")
	errUnsupportedVolumeAccessMode = errors.New("failed to get result of operation")
	// fallback error presented to the user in unexpected situations
	errUnexpected = errors.New("An unexpected error occurred. Please try again, and if the problem persists, contact support@crusoecloud.com.")

	supportedBlockVolumeAccessMode = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:        {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:   {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:    {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER:  {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER: {},
	}
	supportedMountVolumeAccessMode = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:        {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:   {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:    {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER:  {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER: {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:   {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:  {},
	}
)

// UnpackAPIError takes a swagger API error and safely attempts to extract any additional information
// present in the response. The original error is returned unchanged if it cannot be unpacked.
func UnpackAPIError(original error) error {
	apiErr := &swagger.GenericSwaggerError{}
	if ok := errors.As(original, apiErr); !ok {
		return original
	}

	var model apiError
	err := json.Unmarshal(apiErr.Body(), &model)
	if err != nil {
		return original
	}

	// some error messages are of the format "rpc code = ... desc = ..."
	// in those cases, we extract the description and return it
	const two = 2
	components := strings.Split(model.Message, " desc = ")
	if len(components) == two {
		//nolint:goerr113 // error is dynamic
		return fmt.Errorf("%s", components[1])
	}

	//nolint:goerr113 // error is dynamic
	return fmt.Errorf("%s", model.Message)
}

func opResultToError(res interface{}) (expectedErr, unexpectedErr error) {
	b, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal operation error: %w", err)
	}
	resultError := opResultError{}
	err = json.Unmarshal(b, &resultError)
	if err != nil {
		return nil, fmt.Errorf("op result type not error as expected: %w", err)
	}

	//nolint:goerr113 //This function is designed to return dynamic errors
	return fmt.Errorf("%s", resultError.Message), nil
}

func parseOpResult[T any](opResult interface{}) (*T, error) {
	b, err := json.Marshal(opResult)
	if err != nil {
		return nil, errUnableToGetOpRes
	}

	var result T
	err = json.Unmarshal(b, &result)
	if err != nil {
		return nil, errUnableToGetOpRes
	}

	return &result, nil
}

// awaitOperation polls an async API operation until it resolves into a success or failure state.
func awaitOperation(ctx context.Context, op *crusoeapi.Operation, projectID string,
	getFunc func(context.Context, string, string) (crusoeapi.Operation, *http.Response, error)) (
	*crusoeapi.Operation, error,
) {
	for op.State == string(OpInProgress) {
		updatedOps, httpResp, err := getFunc(ctx, projectID, op.OperationId)
		if err != nil {
			return nil, fmt.Errorf("error getting operation with id %s: %w", op.OperationId, err)
		}
		httpResp.Body.Close()

		op = &updatedOps

		time.Sleep(pollInterval)
	}

	switch op.State {
	case string(OpSucceeded):
		return op, nil
	case string(OpFailed):
		opError, err := opResultToError(op.Result)
		if err != nil {
			return op, err
		}

		return op, opError
	default:

		return op, errUnexpected
	}
}

// AwaitOperationAndResolve awaits an async API operation and attempts to parse the response as an instance of T,
// if the operation was successful.
func awaitOperationAndResolve[T any](ctx context.Context, op *crusoeapi.Operation, projectID string,
	getFunc func(context.Context, string, string) (crusoeapi.Operation, *http.Response, error),
) (*T, *crusoeapi.Operation, error) {
	op, err := awaitOperation(ctx, op, projectID, getFunc)
	if err != nil {
		return nil, op, err
	}

	result, err := parseOpResult[T](op.Result)
	if err != nil {
		return nil, op, err
	}

	return result, op, nil
}

func createDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID string, createReq crusoeapi.DisksPostRequestV1Alpha5) (*crusoeapi.DiskV1Alpha5, error) {
	dataResp, httpResp, err := apiClient.DisksApi.CreateDisk(ctx, createReq, projectID)
	if err != nil {
		return nil, fmt.Errorf("Failed to start a create disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	disk, _, err := awaitOperationAndResolve[crusoeapi.DiskV1Alpha5](ctx, dataResp.Operation, projectID, apiClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk: %w", err)
	}

	return disk, nil
}

func attachDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, vmID string, attachReq crusoeapi.InstancesAttachDiskPostRequestV1Alpha5) error {
	dataResp, httpResp, err := apiClient.VMsApi.UpdateInstanceAttachDisks(ctx, attachReq, projectID, vmID)
	if err != nil {
		return fmt.Errorf("Failed to start an attach disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	_, err = awaitOperation(ctx, dataResp.Operation, projectID, apiClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		return fmt.Errorf("failed to attach disk: %w", err)
	}

	return nil
}

func detachDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, vmID string, detachReq crusoeapi.InstancesDetachDiskPostRequest) error {
	dataResp, httpResp, err := apiClient.VMsApi.UpdateInstanceDetachDisks(ctx, detachReq, projectID, vmID)
	if err != nil {
		return fmt.Errorf("Failed to start a detach disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	_, err = awaitOperation(ctx, dataResp.Operation, projectID, apiClient.DiskOperationsApi.GetStorageDisksOperation)
	if err != nil {
		return fmt.Errorf("failed to detach disk: %w", err)
	}

	return nil
}

func updateDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, diskID string, updateReq crusoeapi.DisksPatchRequest) (*crusoeapi.DiskV1Alpha5, error) {
	dataResp, httpResp, err := apiClient.DisksApi.ResizeDisk(ctx, updateReq, projectID, diskID)
	if err != nil {
		return nil, fmt.Errorf("Failed to start a create disk operation: %w", err)
	}
	defer httpResp.Body.Close()

	disk, _, err := awaitOperationAndResolve[crusoeapi.DiskV1Alpha5](ctx, dataResp.Operation, projectID, apiClient.DiskOperationsApi.GetStorageDisksOperation)
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

func findDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, name string) (*crusoeapi.DiskV1Alpha5, error) {
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

func getDisk(ctx context.Context, apiClient *crusoeapi.APIClient, projectID, diskID string) (*crusoeapi.DiskV1Alpha5, error) {
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
		return 0, fmt.Errorf("invalid numeric value: %v", err)
	}

	var totalBytes int64
	switch unit {
	case "GiB":
		totalBytes = int64(value * BytesInGiB)
	case "TiB":
		totalBytes = int64(value * BytesInTiB)
	default:
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

	return &csi.Volume{
		CapacityBytes:      volBytes,
		VolumeId:           disk.Id,
		VolumeContext:      nil,
		ContentSource:      nil,
		AccessibleTopology: []*csi.Topology{accessibleTopology},
	}, nil
}

func validateVolumeCapabilities(capabilities []*csi.VolumeCapability) error {
	for _, capability := range capabilities {
		if capability.GetBlock() != nil && capability.GetMount() != nil {
			return fmt.Errorf("neither block nor mount capability specified")
		}
		if capability.GetBlock() == nil && capability.GetMount() == nil {
			return fmt.Errorf("both block and mount capabilities specified")
		}

		accessMode := capability.GetAccessMode().GetMode()
		if capability.GetBlock() != nil {
			if _, ok := supportedBlockVolumeAccessMode[accessMode]; !ok {
				return fmt.Errorf("unsupported access mode for block volume: %s", accessMode)
			}
		}
		if capability.GetMount() != nil {
			if _, ok := supportedMountVolumeAccessMode[accessMode]; !ok {
				return fmt.Errorf("unsupported access mode for mount volume: %s", accessMode)
			}
		}
	}

	return nil
}

func getDiskTypFromVolumeType(capabilities []*csi.VolumeCapability) string {
	for _, capability := range capabilities {
		if capability.GetMount() != nil {
			return mountVolumeDiskType
		} else if capability.GetBlock() != nil {
			return blockVolumeDiskType
		}
	}

	return ""
}

func getCreateDiskRequest(name, capacity, location string, capabilities []*csi.VolumeCapability) *crusoeapi.DisksPostRequestV1Alpha5 {
	params := &crusoeapi.DisksPostRequestV1Alpha5{
		Name:     name,
		Size:     capacity,
		Location: location,
	}

	params.Type_ = getDiskTypFromVolumeType(capabilities)

	return params
}

func verifyExistingDisk(currentDisk *crusoeapi.DiskV1Alpha5, createReq *crusoeapi.DisksPostRequestV1Alpha5) error {
	if currentDisk.Size != createReq.Size {
		return fmt.Errorf("disk has different size")
	}
	if currentDisk.Name != createReq.Name {
		return fmt.Errorf("disk has different name")
	}
	if currentDisk.Location != createReq.Location {
		return fmt.Errorf("disk has different location")
	}
	if currentDisk.BlockSize != createReq.BlockSize {
		return fmt.Errorf("disk has different block size")
	}
	if currentDisk.Type_ != createReq.Type_ {
		return fmt.Errorf("disk has different type")
	}

	return nil
}

func parseCapacity(capacityRange *csi.CapacityRange) string {
	reqBytes := capacityRange.GetRequiredBytes()
	if reqBytes == 0 {
		reqBytes = capacityRange.GetLimitBytes()
	}
	reqCapacity := convertBytesToStorageUnit(capacityRange.GetRequiredBytes())

	return reqCapacity
}

func getInstanceIDFromNodeID(nodeID string) string {
	return nodeID
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

	}

	return "", fmt.Errorf("%w: %s", errUnsupportedVolumeAccessMode, accessMode.String())
}