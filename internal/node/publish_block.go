package node

import (
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/mount-utils"
	"os"
	"path/filepath"
)

func nodePublishBlockVolume(serialNumber string,
	mounter *mount.SafeFormatAndMount,
	mountOpts []string,
	request *csi.NodePublishVolumeRequest,
) error {
	dirPath := filepath.Dir(request.GetTargetPath())

	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(dirPath, newDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	// Check if the block volume file exists
	_, err := os.Stat(request.GetTargetPath())
	if errors.Is(err, os.ErrNotExist) {
		// Expose the block volume as a file
		f, openErr := os.OpenFile(request.GetTargetPath(), os.O_CREATE|os.O_EXCL, os.FileMode(newFilePerms))
		if openErr != nil {
			return fmt.Errorf("failed to make file for target path: %w", err)
		}
		if err = f.Close(); err != nil {
			return fmt.Errorf("failed to close file after making target path: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check if target path exists: %w", err)
	}

	devicePath := getSSDDevicePath(serialNumber)

	mountOpts = append(mountOpts, "bind")
	mountOpts = append(mountOpts, request.GetVolumeCapability().GetMount().GetMountFlags()...)
	err = mounter.Mount(devicePath, request.GetTargetPath(), "", mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}
