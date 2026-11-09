package controller

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
)

var errTransient = errors.New("connection reset")

func TestCodeForOperationError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		name string
		want codes.Code
	}{
		{
			name: "operation still in progress is retryable",
			err:  fmt.Errorf("operation op-1 %w after 5m0s", common.ErrOperationInProgress),
			want: codes.DeadlineExceeded,
		},
		{
			name: "other operation errors stay internal",
			err:  fmt.Errorf("error getting operation with id op-1: %w", errTransient),
			want: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := codeForOperationError(tt.err); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
