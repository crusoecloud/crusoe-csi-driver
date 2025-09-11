package node

import (
	"errors"
	"fmt"
	"os"

	"k8s.io/klog/v2"

	"k8s.io/mount-utils"
)

var (
	errPathEmpty      = errors.New("target path is empty")
	errNothingMounted = errors.New("nothing mounted at target path")
	errDeviceMismatch = errors.New("device mismatch")
)

func isMountPointQuick(mounter *mount.SafeFormatAndMount, targetPath string) (bool, error) {
	// May suggest a mount is not present when it is.
	// Will not suggest a mount is present when it is not.
	//
	// TL;DR
	// true: potentially not a mount point
	// false: definitely is a mount point
	isLikelyNotMountPoint, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		return false, err
	}

	if !isLikelyNotMountPoint {
		return true, nil
	}

	// Exhaustively check if targetPath is a mount point
	isMountPoint, err := mounter.IsMountPoint(targetPath)
	if err != nil {
		return false, err
	}

	return isMountPoint, nil
}

func verifyMountedVolumeWithUtilsHelper(mounter *mount.SafeFormatAndMount, targetPath, deviceFullPath string) error {
	// isMountPointQuick fails if the target path does not exist, check that first
	_, statErr := os.Stat(targetPath)
	if os.IsNotExist(statErr) {
		return errPathEmpty
	} else if statErr != nil {
		return fmt.Errorf("failed to check if target path %s exists: %w", targetPath, statErr)
	}

	isMountPoint, err := isMountPointQuick(mounter, targetPath)
	if err != nil {
		return fmt.Errorf("failed to check if target path %s is a mount point: %w", targetPath, err)
	}

	if !isMountPoint {
		// Nothing is mounted at the target path
		return errNothingMounted
	}

	// Use mount-utils to resolve device names properly
	actualDeviceFullPath, _, err := mount.GetDeviceNameFromMount(mounter, targetPath)
	if err != nil {
		return fmt.Errorf("failed to get device name from mount: %w", err)
	}

	// TODO: removeme
	klog.Warningf("actualDeviceFullPath: %s, deviceFullPath: %s", actualDeviceFullPath, deviceFullPath)

	if actualDeviceFullPath != deviceFullPath {
		return fmt.Errorf("%w: expected %s, got %s", errDeviceMismatch, deviceFullPath, actualDeviceFullPath)
	}

	return nil
}

// verifyMountedVolumeWithUtils checks if the desired volume is mounted at the target path.
func verifyMountedVolumeWithUtils(mounter *mount.SafeFormatAndMount, targetPath, deviceFullPath string) (bool, error) {
	// Idempotency check: exit early if the disk is already mounted to the target path
	verifyErr := verifyMountedVolumeWithUtilsHelper(mounter, targetPath, deviceFullPath)

	switch {
	// Disk is already mounted to the target path, exit early
	case verifyErr == nil:
		return true, nil
	// Target path is empty, continue mounting the disk
	case errors.Is(verifyErr, errPathEmpty):
		return false, nil
	// Nothing is mounted at the target path, continue mounting the disk
	case errors.Is(verifyErr, errNothingMounted):
		return false, nil

	// Another disk is mounted at the target path, unmount the existing disk and continue mounting the disk
	case errors.Is(verifyErr, errDeviceMismatch):
		unmountErr := mounter.Unmount(targetPath)
		if unmountErr != nil {
			return false, fmt.Errorf("failed to cleanup existing mount at %s", targetPath)
		}

		return false, nil

	// Failed to verify mounted volume
	default:
		return false, verifyErr
	}
}
