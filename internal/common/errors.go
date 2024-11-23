package common

import "errors"

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrTimeout        = errors.New("timeout")
)
