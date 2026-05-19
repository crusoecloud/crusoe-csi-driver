// Package fs export hooks for in-package tests.
//
// This file uses the conventional _test.go suffix so it is only compiled into
// the test binary. It exposes package-private symbols (materializeNFSTarget,
// lookupIP) under exported names so resolve_test.go can live in the fs_test
// (black-box) package alongside the other tests in this directory.
package fs

import "net"

// MaterializeNFSTarget is a test-only exported handle for materializeNFSTarget.
//
//nolint:gochecknoglobals // deliberate test seam
var MaterializeNFSTarget = materializeNFSTarget

// SetLookupIP swaps the package-private DNS lookup function for the duration
// of a test and returns the previous value so callers can restore it via
// t.Cleanup or defer.
func SetLookupIP(fn func(host string) ([]net.IP, error)) func(host string) ([]net.IP, error) {
	prev := lookupIP
	lookupIP = fn

	return prev
}

// ErrNoUsableNFSAddressForTest re-exports the sentinel error from resolve.go
// so tests can match it with errors.Is without taking a package-private
// dependency.
var ErrNoUsableNFSAddressForTest = ErrNoUsableNFSAddress
