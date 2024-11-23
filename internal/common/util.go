package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

type OpStatus string

type OpResultErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	PollInterval          = 1 * time.Second
	OpSucceeded  OpStatus = "SUCCEEDED"
	OpInProgress OpStatus = "IN_PROGRESS"
	OpFailed     OpStatus = "FAILED"
)

const numExpectedComponents = 2

var (
	ErrUnableToGetOpRes         = errors.New("failed to get result of operation")
	ErrUnexpectedOperationState = errors.New("unexpected operation state")
	ErrNoSizeRequested          = errors.New("no disk size requested")
)

func CancellableSleep(ctx context.Context, duration time.Duration) error {
	t := time.NewTimer(duration)
	select {
	case <-ctx.Done():
		t.Stop()

		return ErrTimeout
	case <-t.C:
	}

	return nil
}

func OpResultToError(res interface{}) (expectedErr, unexpectedErr error) {
	b, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal operation error: %w", err)
	}
	resultError := OpResultErr{}
	err = json.Unmarshal(b, &resultError)
	if err != nil {
		return nil, fmt.Errorf("op result type not error as expected: %w", err)
	}

	//nolint:goerr113 // error is intentionally dynamic
	return fmt.Errorf("%s", resultError.Message), nil
}

// AwaitOperation polls an async API operation until it resolves into a success or failure state.
func AwaitOperation(ctx context.Context, op *crusoeapi.Operation, projectID string,
	getOp func(ctx context.Context, projectID string, operationID string) (crusoeapi.Operation, *http.Response, error),
) (
	*crusoeapi.Operation, error,
) {
	timeoutCtx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()

	for op.State == string(OpInProgress) {
		updatedOp, _, err := getOp(timeoutCtx, projectID, op.OperationId)
		if err != nil {
			return nil, fmt.Errorf("error getting operation with id %s: %w", op.OperationId, err)
		}

		op = &updatedOp

		err = CancellableSleep(timeoutCtx, PollInterval)
		if err != nil {
			return nil, err
		}
	}

	switch op.State {
	case string(OpSucceeded):
		return op, nil
	case string(OpFailed):
		opError, err := OpResultToError(op.Result)
		if err != nil {
			return nil, err
		}

		return nil, fmt.Errorf("operation failed: %w", opError)
	default:

		return nil, fmt.Errorf("%w: %s", ErrUnexpectedOperationState, op.State)
	}
}

func GetAsyncOperationResult[T any](ctx context.Context, op *crusoeapi.Operation, projectID string,
	getOp func(ctx context.Context, projectID string, operationID string) (crusoeapi.Operation, *http.Response, error),
) (*T, *crusoeapi.Operation, error) {
	completedOp, err := AwaitOperation(ctx, op, projectID, getOp)
	if err != nil {
		return nil, nil, err
	}

	b, err := json.Marshal(completedOp.Result)
	if err != nil {
		return nil, completedOp, fmt.Errorf("%w: could not marshal operation result: %w", ErrUnableToGetOpRes, err)
	}

	var result T
	err = json.Unmarshal(b, &result)
	if err != nil {
		return nil, completedOp, fmt.Errorf("%w: could not unmarshal operation result: %w", ErrUnableToGetOpRes, err)
	}

	return &result, completedOp, nil
}

// UnpackSwaggerErr takes a swagger error and safely attempts to extract the
// additional information which is present in the response. The error
// is returned unchanged if it cannot be unpacked.
func UnpackSwaggerErr(original error) error {
	swagErr := &crusoeapi.GenericSwaggerError{}
	if ok := errors.As(original, swagErr); !ok {
		return original
	}

	var model crusoeapi.ErrorBody
	err := json.Unmarshal(swagErr.Body(), &model)
	if err != nil {
		return original
	}

	// some error messages are of the format "rpc code = ... desc = ..."
	// in those cases, we extract the description and return it
	components := strings.Split(model.Message, " desc = ")
	if len(components) == numExpectedComponents {
		//nolint:goerr113 // error is intentionally dynamic
		return fmt.Errorf("%s", components[1])
	}

	//nolint:goerr113 // error is intentionally dynamic
	return fmt.Errorf("%s", model.Message)
}

func RequestSizeToBytes(capacityRange *csi.CapacityRange) (int64, error) {
	var requestSizeBytes int64

	switch {
	case capacityRange.GetRequiredBytes() != 0:
		requestSizeBytes = capacityRange.GetRequiredBytes()
	case capacityRange.GetLimitBytes() != 0:
		requestSizeBytes = capacityRange.GetLimitBytes()
	default:
		return 0, ErrNoSizeRequested
	}

	return requestSizeBytes, nil
}

func RequestSizeToGiB(capacityRange *csi.CapacityRange) (int, error) {
	requestSizeBytes, err := RequestSizeToBytes(capacityRange)
	if err != nil {
		return 0, err
	}

	requestSizeGiB := int(math.Ceil(float64(requestSizeBytes) / float64(NumBytesInGiB)))

	return requestSizeGiB, nil
}

func GetTopologyKey(pluginName, key string) string {
	return fmt.Sprintf("%s/%s", pluginName, key)
}

func TrimPVCPrefix(pvcName string) string {
	return strings.TrimPrefix(pvcName, "pvc-")
}
