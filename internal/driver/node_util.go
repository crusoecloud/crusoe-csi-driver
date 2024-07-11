package driver

import (
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/mount-utils"
	"os"
	"path/filepath"
)

func getPersistentSSDDevicePath(serialNumber string) string {
	// symlink: /dev/disk/by-id/virtio-<serial-number>
	return fmt.Sprintf("/dev/disk/by-id/virtio-%s", serialNumber)
}

func publishBlockVolume(req *csi.NodePublishVolumeRequest, targetPath string,
	mounter *mount.SafeFormatAndMount, mountOpts []string,
) error {
	volumeContext := req.GetVolumeContext()
	serialNumber, ok := volumeContext[VolumeContextDiskSerialNumberKey]
	if !ok {
		return errVolumeMissingSerialNumber
	}

	devicePath := getPersistentSSDDevicePath(serialNumber)
	dirPath := filepath.Dir(targetPath)
	// Check if the directory exists
	if _, err := os.Stat(dirPath); errors.Is(err, os.ErrNotExist) {
		// Directory does not exist, create it
		if err := os.MkdirAll(dirPath, newDirPerms); err != nil {
			return fmt.Errorf("failed to make directory for target path: %w", err)
		}
	}

	// expose the block volume as a file
	f, err := os.OpenFile(targetPath, os.O_CREATE, os.FileMode(newFilePerms))
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to make file for target path: %w", err)
		}
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("failed to close file after making target path: %w", err)
	}

	err = mounter.FormatAndMount(devicePath, targetPath, "", mountOpts)
	if err != nil {
		return fmt.Errorf("failed to mount volume at target path: %w", err)
	}

	return nil
}
