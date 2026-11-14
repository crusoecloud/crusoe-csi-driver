package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

// testTimeout stands in for OperationTimeout. Polling runs at PollInterval
// (1s), so the timeout lands during the second sleep.
const testTimeout = 1500 * time.Millisecond

var errStatusRead = errors.New("status read failed")

// honorCtx makes getOp fail on a done context with no response, as the real
// HTTP client does.
func honorCtx(getOp getOperationFunc) getOperationFunc {
	return func(ctx context.Context, projectID, id string) (crusoeapi.Operation, *http.Response, error) {
		if err := ctx.Err(); err != nil {
			return crusoeapi.Operation{}, nil, fmt.Errorf("get operation %s: %w", id, err)
		}

		return getOp(ctx, projectID, id)
	}
}

// readResult is one scripted getOp response. An empty state means the read fails.
type readResult struct {
	result interface{}
	state  string
}

// scriptedGetOp returns the reads in order, repeating the last one, and counts the calls.
func scriptedGetOp(reads ...readResult) (getOp getOperationFunc, calls *int) {
	calls = new(int)

	return honorCtx(func(_ context.Context, _, id string) (crusoeapi.Operation, *http.Response, error) {
		r := reads[min(*calls, len(reads)-1)]
		*calls++

		if r.state == "" {
			return crusoeapi.Operation{}, nil, errStatusRead
		}

		op := crusoeapi.Operation{OperationId: id, State: r.state}
		if r.result != nil {
			op.Result = &r.result
		}

		return op, nil, nil
	}), calls
}

func readInProgress() readResult {
	return readResult{state: string(OpInProgress)}
}

func readSucceeded() readResult {
	return readResult{state: string(OpSucceeded)}
}

func readFailed() readResult {
	return readResult{}
}

func inProgressOp() *crusoeapi.Operation {
	return &crusoeapi.Operation{OperationId: "op-123", State: string(OpInProgress)}
}

func TestAwaitOperationReturnsTerminalState(t *testing.T) {
	t.Parallel()

	t.Run("failed operation returns its message", func(t *testing.T) {
		t.Parallel()

		getOp, _ := scriptedGetOp(readResult{
			state:  string(OpFailed),
			result: OpResultErr{Message: "no NFS traffic manager found"},
		})

		_, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, testTimeout)
		if err == nil || err.Error() != "operation failed: no NFS traffic manager found" {
			t.Fatalf("expected the operation's failure message, got %v", err)
		}
	})

	t.Run("operation that finishes while polling succeeds", func(t *testing.T) {
		t.Parallel()

		getOp, _ := scriptedGetOp(readInProgress(), readSucceeded())

		op, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, 5*time.Second)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if op.State != string(OpSucceeded) {
			t.Fatalf("expected state %q, got %q", OpSucceeded, op.State)
		}
	})
}

func TestAwaitOperationTimeoutIsInProgress(t *testing.T) {
	t.Parallel()

	getOp, _ := scriptedGetOp(readInProgress())

	_, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, testTimeout)
	if !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("expected ErrOperationInProgress, got %v", err)
	}
	if !strings.Contains(err.Error(), "operation op-123 still in progress after 1.5s") {
		t.Fatalf("unexpected message: %v", err)
	}
}

func TestAwaitOperationCallerGone(t *testing.T) {
	t.Parallel()

	// The caller's own deadline ends before ours, so its error comes back
	// rather than a timeout it did not cause.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(cancel)

	getOp, _ := scriptedGetOp(readInProgress())

	_, err := awaitOperation(ctx, inProgressOp(), "proj-1", getOp, testTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("caller timeout must not be reported as in progress: %v", err)
	}
}

func TestAwaitOperationReadErrorRetries(t *testing.T) {
	t.Parallel()

	getOp, calls := scriptedGetOp(readFailed(), readSucceeded())

	op, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, 5*time.Second)
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if op.State != string(OpSucceeded) {
		t.Fatalf("expected state %q, got %q", OpSucceeded, op.State)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 status reads, got %d", *calls)
	}
}

func TestAwaitOperationReadErrorsUntilTimeout(t *testing.T) {
	t.Parallel()

	// Reads that keep failing end as a timeout, not as the read error.
	getOp, _ := scriptedGetOp(readFailed())

	_, err := awaitOperation(context.Background(), inProgressOp(), "proj-1", getOp, testTimeout)
	if !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("expected ErrOperationInProgress, got %v", err)
	}
}
