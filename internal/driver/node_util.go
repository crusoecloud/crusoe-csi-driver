package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
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
	f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL, os.FileMode(newFilePerms))
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to make file for target path: %w", err)
		}
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("failed to close file after making target path: %w", err)
	}

	mountOpts = append(mountOpts, "bind")
	err = mounter.Mount(devicePath, targetPath, "", mountOpts)
	if err != nil {
		return fmt.Errorf("failed to mount volume at target path: %w", err)
	}

	return nil
}

func publishFilesystemVolume(req *csi.NodePublishVolumeRequest, targetPath string,
	mounter *mount.SafeFormatAndMount, mountOpts []string,
) error {
	volumeContext := req.GetVolumeContext()
	serialNumber, ok := volumeContext[VolumeContextDiskSerialNumberKey]
	if !ok {
		return errVolumeMissingSerialNumber
	}

	devicePath := getPersistentSSDDevicePath(serialNumber)

	// Check if the directory exists
	if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
		// Directory does not exist, create it
		if mkdirErr := os.MkdirAll(targetPath, newDirPerms); mkdirErr != nil {
			return fmt.Errorf("failed to make directory for target path: %w", mkdirErr)
		}
	}

	volumeCapability := req.GetVolumeCapability()
	fsType := volumeCapability.GetMount().GetFsType()
	mountOpts = append(mountOpts, volumeCapability.GetMount().GetMountFlags()...)
	err := mounter.FormatAndMount(devicePath, targetPath, fsType, mountOpts)
	if err != nil {
		return status.Errorf(codes.Internal,
			fmt.Sprintf("failed to mount volume at target path: %s", err.Error()))
	}

	return nil
}
