package crusoe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"net/http"
	"time"
)

type opStatus string

type opResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	pollInterval          = 1 * time.Second
	OpSucceeded  opStatus = "SUCCEEDED"
	OpInProgress opStatus = "IN_PROGRESS"
	OpFailed     opStatus = "FAILED"
)

var (
	errUnableToGetOpRes = errors.New("failed to get result of operation")
)

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

// awaitOperation polls an async API operation until it resolves into a success or failure state.
func awaitOperation(ctx context.Context, op *crusoeapi.Operation, projectID string,
	getOp func(ctx context.Context, projectID string, operationID string) (crusoeapi.Operation, *http.Response, error)) (
	*crusoeapi.Operation, error,
) {
	for op.State == string(OpInProgress) {
		updatedOp, _, err := getOp(ctx, projectID, op.OperationId)
		if err != nil {
			return nil, fmt.Errorf("error getting operation with id %s: %w", op.OperationId, err)
		}

		op = &updatedOp

		time.Sleep(pollInterval)
	}

	switch op.State {
	case string(OpSucceeded):
		return op, nil
	case string(OpFailed):
		opError, err := opResultToError(op.Result)
		if err != nil {
			return nil, err
		}

		return nil, opError
	default:

		return nil, fmt.Errorf("unexpected operation state: %s", op.State)
	}
}

func getAsyncOperationResult[T any](ctx context.Context, op *crusoeapi.Operation, projectID string,
	getOp func(ctx context.Context, projectID string, operationID string) (crusoeapi.Operation, *http.Response, error),
) (*T, *crusoeapi.Operation, error) {
	completedOp, err := awaitOperation(ctx, op, projectID, getOp)
	if err != nil {
		return nil, nil, err
	}

	b, err := json.Marshal(completedOp.Result)
	if err != nil {
		return nil, completedOp, fmt.Errorf("%w: could not marshal operation result: %w", errUnableToGetOpRes, err)
	}

	var result T
	err = json.Unmarshal(b, &result)
	if err != nil {
		return nil, completedOp, fmt.Errorf("%w: could not unmarshal operation result: %w", errUnableToGetOpRes, err)
	}

	return &result, completedOp, nil
}
