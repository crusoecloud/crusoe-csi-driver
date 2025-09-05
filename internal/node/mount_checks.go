package node

import (
	"errors"
	"fmt"
	"k8s.io/mount-utils"
)

var errNothingMounted = errors.New("nothing mounted at target path")
var errDeviceMismatch = errors.New("device mismatch")

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

// verifyMountedVolumeWithUtils checks if the desired volume is mounted at the target path
func verifyMountedVolumeWithUtils(mounter *mount.SafeFormatAndMount, targetPath, deviceFullPath string) error {
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

	//TODO: removeme
	//klog.Warningf("actualDeviceFullPath: %s, deviceFullPath: %s", actualDeviceFullPath, deviceFullPath)

	if actualDeviceFullPath != deviceFullPath {
		return fmt.Errorf("%w: expected %s, got %s", errDeviceMismatch, deviceFullPath, actualDeviceFullPath)
	}

	return nil
}
