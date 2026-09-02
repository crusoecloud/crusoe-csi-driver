// Package fs export hooks for in-package tests.
//
// This file uses the conventional _test.go suffix so it is only compiled into
// the test binary. It exposes package-private symbols (materializeNFSTarget,
// lookupIP) under exported names so resolve_test.go can live in the fs_test
// (black-box) package alongside the other tests in this directory.
package fs

import (
	"context"
	"net"

	"k8s.io/mount-utils"
)

// StageMountForTest exposes the package-private stageMount (option assembly + the
// real per-node mount, without the platform-specific already-mounted pre-check) so
// the fs_test package can assert the assembled mount options with a mock mounter.
func StageMountForTest(
	mounter *mount.SafeFormatAndMount,
	nfsEnabled bool,
	nfsRemotePorts, devicePath, stagingTargetPath string,
	mountFlags []string,
) error {
	return stageMount(mounter, nfsEnabled, nfsRemotePorts, devicePath, stagingTargetPath, mountFlags)
}

// BindMountForTest exposes the package-private bindMount (the per-pod bind, without
// the platform-specific mount-point pre-check) so the fs_test package can assert
// the bind options with a mock mounter.
func BindMountForTest(
	mounter *mount.SafeFormatAndMount,
	stagingTargetPath, targetPath string,
	readonly bool,
	mountFlags []string,
) error {
	return bindMount(mounter, stagingTargetPath, targetPath, readonly, mountFlags)
}

// MaterializeNFSTarget is a test-only exported handle for materializeNFSTarget.
//
//nolint:gochecknoglobals // deliberate test seam
var MaterializeNFSTarget = materializeNFSTarget

// SetLookupIP swaps the package-private DNS lookup function for the duration of
// a test and returns a restore func the caller must defer (or register with
// t.Cleanup). Returning the restore closure — rather than the previous value —
// makes the restoration obligation explicit, so a caller can't accidentally
// leave package state mutated for later tests in the same binary. The signature
// mirrors net.Resolver.LookupIP so tests can assert the network ("ip4") and FQDN
// host passed by materializeNFSTarget.
func SetLookupIP(
	fn func(ctx context.Context, network, host string) ([]net.IP, error),
) (restore func()) {
	prev := lookupIP
	lookupIP = fn

	return func() { lookupIP = prev }
}

// ErrNoUsableNFSAddressForTest re-exports the sentinel error from resolve.go
// so tests can match it with errors.Is without taking a package-private
// dependency.
var ErrNoUsableNFSAddressForTest = ErrNoUsableNFSAddress

// Test-only re-exports of package-private identifiers so the fs_test package can
// assert against them without hardcoding values.
const (
	DNSFallbackLocation   = dnsFallbackLocation
	CrusoeCloudDNSNFSHost = crusoeCloudDNSNFSHost
	DNSRemotePorts        = dnsRemotePorts
)

// ResolveNFSTargetForTest exposes the unexported resolveNFSTarget method so the
// fs_test package can exercise the FF-off / FF-on / fallback branching.
func (d *Node) ResolveNFSTargetForTest(
	ctx context.Context, volumeID string, nfsEnabled bool,
) (host, remotePorts string) {
	return d.resolveNFSTarget(ctx, volumeID, nfsEnabled)
}
