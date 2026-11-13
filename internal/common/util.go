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
	"k8s.io/klog/v2"
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

// While status reads keep failing, the wait between them grows by pollBackoffFactor
// up to maxPollBackoff.
const (
	pollBackoffFactor = 2
	maxPollBackoff    = 15 * time.Second
)

const numExpectedComponents = 2

func CancellableSleep(ctx context.Context, duration time.Duration) error {
	t := time.NewTimer(duration)
	select {
	case <-ctx.Done():
		t.Stop()

		return fmt.Errorf("context cancelled: %w", ctx.Err())
	case <-t.C:
		// Sleep time elapsed, return control to caller
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

type getOperationFunc func(ctx context.Context, projectID string, operationID string) (
	crusoeapi.Operation, *http.Response, error)

// AwaitOperation polls an async API operation until it reaches a terminal state
// or OperationTimeout runs out.
func AwaitOperation(ctx context.Context, op *crusoeapi.Operation, projectID string,
	getOp func(ctx context.Context, projectID string, operationID string) (crusoeapi.Operation, *http.Response, error),
) (
	*crusoeapi.Operation, error,
) {
	return awaitOperation(ctx, op, projectID, getOp, OperationTimeout)
}

// awaitOperation is AwaitOperation with the timeout as a parameter, so tests can use a short one.
// A timeout is reported as ErrOperationInProgress, which the controllers map to
// codes.DeadlineExceeded so the sidecars retry.
func awaitOperation(ctx context.Context, op *crusoeapi.Operation, projectID string,
	getOp getOperationFunc, timeout time.Duration,
) (
	*crusoeapi.Operation, error,
) {
	// Child context, so the wait is bounded without touching the caller's context.
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Normal poll rate. Back off only while status reads keep failing.
	wait := PollInterval
	for op.State == string(OpInProgress) {
		updated, resp, err := getOp(timeoutCtx, projectID, op.OperationId)
		switch {
		case err != nil && isPermanentReadError(resp):
			// The request itself was rejected (operation not found, no access), so
			// asking again cannot succeed.
			return nil, fmt.Errorf("error getting operation with id %s: %w", op.OperationId, err)
		case err != nil:
			// A failed status read says nothing about the operation, which may
			// still be running. Log it and try again instead of failing the call.
			klog.Errorf("failed to read operation %s: %v", op.OperationId, err)
			wait = min(wait*pollBackoffFactor, maxPollBackoff)
		default:
			op = &updated
			wait = PollInterval
		}

		// The sleep is cut short when the budget runs out or the caller cancels.
		// That is the loop's only other exit: a completed sleep means poll again.
		if CancellableSleep(timeoutCtx, wait) != nil {
			break
		}
	}

	// The operation settled before the budget ran out, so report what it says.
	if op.State != string(OpInProgress) {
		return operationResult(op)
	}

	// Still in progress, but the caller is gone. Hand back its own cancellation
	// rather than a timeout it did not cause.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("operation %s: %w", op.OperationId, ctx.Err())
	}

	// The caller is still waiting, so the sleep was cut short by our own timeout.
	return nil, fmt.Errorf("operation %s %w after %s", op.OperationId, ErrOperationInProgress, timeout)
}

// isPermanentReadError reports whether a failed status read was a 4xx that a retry
// cannot fix. 408 and 429 are temporary, and no response at all (network error,
// expired context) is treated as temporary too.
func isPermanentReadError(resp *http.Response) bool {
	if resp == nil {
		return false
	}

	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	default:
		return resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError
	}
}

func operationResult(op *crusoeapi.Operation) (*crusoeapi.Operation, error) {
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

func GetUserAgent() string {
	return fmt.Sprintf("%s/%s", PluginName, PluginVersion)
}
