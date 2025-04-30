package common

import "errors"

var (
	ErrNotImplemented           = errors.New("not implemented")
	ErrUnableToGetOpRes         = errors.New("failed to get result of operation")
	ErrUnexpectedOperationState = errors.New("unexpected operation state")
	ErrNoSizeRequested          = errors.New("no disk size requested")
)
