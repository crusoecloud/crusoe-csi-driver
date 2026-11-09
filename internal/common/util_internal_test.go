package common

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

// testTimeout stands in for OperationTimeout. Polling runs at PollInterval
// (1s), so the timeout lands between the second poll and the following sleep.
const testTimeout = 1500 * time.Millisecond

var errStatusUnavailable = errors.New("status endpoint unavailable")

// opAfterTimeout returns state while polling and finalState once the test
// timeout has passed, which is when the final check runs.
func opAfterTimeout(start time.Time, finalState string, result interface{}) getOperationFunc {
	return func(_ context.Context, _, id string) (crusoeapi.Operation, *http.Response, error) {
		if time.Since(start) < testTimeout {
			return crusoeapi.Operation{OperationId: id, State: string(OpInProgress)}, nil, nil
		}

		op := crusoeapi.Operation{OperationId: id, State: finalState}
		if result != nil {
			op.Result = &result
		}

		return op, nil, nil
	}
}

func failResult(msg string) interface{} {
	return OpResultErr{Message: msg}
}

func inProgressOp() *crusoeapi.Operation {
	return &crusoeapi.Operation{OperationId: "op-123", State: string(OpInProgress)}
}

func TestAwaitOperationFinalCheckAfterTimeout(t *testing.T) {
	t.Parallel()

	tests := []finalCheckCase{
		{
			name:        "operation failed by the final check returns its message",
			finalState:  string(OpFailed),
			result:      failResult("no NFS traffic manager found"),
			wantMsgPart: "operation failed: no NFS traffic manager found",
		},
		{
			name:        "operation still running at the final check is reported as in progress",
			finalState:  string(OpInProgress),
			wantErr:     ErrOperationInProgress,
			wantMsgPart: "operation op-123 still in progress after 1.5s",
		},
		{
			name:       "operation finished by the final check succeeds",
			finalState: string(OpSucceeded),
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			getOp := opAfterTimeout(time.Now(), tt.finalState, tt.result)
			op, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, testTimeout)
			assertFinalCheck(t, tt, op, err)
		})
	}
}

type finalCheckCase struct {
	result      interface{}
	wantErr     error
	name        string
	finalState  string
	wantMsgPart string
}

func assertFinalCheck(t *testing.T, tt *finalCheckCase, op *crusoeapi.Operation, err error) {
	t.Helper()

	if tt.wantMsgPart == "" {
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if op.State != string(OpSucceeded) {
			t.Fatalf("expected state %q, got %q", OpSucceeded, op.State)
		}

		return
	}

	if err == nil || !strings.Contains(err.Error(), tt.wantMsgPart) {
		t.Fatalf("expected error containing %q, got %v", tt.wantMsgPart, err)
	}
	if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
		t.Fatalf("expected errors.Is(%v), got %v", tt.wantErr, err)
	}
	if tt.wantErr == nil && errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("a terminal failure must not be reported as in progress: %v", err)
	}
}

func TestAwaitOperationFinalCheckError(t *testing.T) {
	t.Parallel()

	start := time.Now()
	getOp := func(_ context.Context, _, id string) (crusoeapi.Operation, *http.Response, error) {
		if time.Since(start) < testTimeout {
			return crusoeapi.Operation{OperationId: id, State: string(OpInProgress)}, nil, nil
		}

		return crusoeapi.Operation{}, nil, errStatusUnavailable
	}

	_, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, testTimeout)
	if !errors.Is(err, errStatusUnavailable) {
		t.Fatalf("expected the status error, got %v", err)
	}
	if !strings.Contains(err.Error(), "error getting operation with id op-123 after 1.5s") {
		t.Fatalf("unexpected message: %v", err)
	}
}

func TestAwaitOperationCallerGoneSkipsFinalCheck(t *testing.T) {
	t.Parallel()

	// The caller's own deadline ends before ours: nobody is waiting, so there
	// is no final check and the context error comes back as before.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(cancel)

	calls := 0
	getOp := func(_ context.Context, _, id string) (crusoeapi.Operation, *http.Response, error) {
		calls++

		return crusoeapi.Operation{OperationId: id, State: string(OpInProgress)}, nil, nil
	}

	_, err := awaitOperation(ctx, inProgressOp(), "proj-1", getOp, testTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("caller timeout must not be reported as in progress: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 status call (no final check), got %d", calls)
	}
}

func TestAwaitOperationResolvesBeforeTimeout(t *testing.T) {
	t.Parallel()

	result := failResult("quota exceeded")
	getOp := func(_ context.Context, _, id string) (crusoeapi.Operation, *http.Response, error) {
		return crusoeapi.Operation{OperationId: id, State: string(OpFailed), Result: &result}, nil, nil
	}

	_, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, testTimeout)
	if err == nil || err.Error() != "operation failed: quota exceeded" {
		t.Fatalf("expected the unchanged failure message, got %v", err)
	}
}
