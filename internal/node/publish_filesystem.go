package node

import (
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"k8s.io/mount-utils"
	"os"
)

func nodePublishFSFilesystemVolume(
	_ string,
	mounter *mount.SafeFormatAndMount,
	_ *mount.ResizeFs,
	mountOpts []string,
	_ common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	volumeContext := request.GetVolumeContext()
	diskName, ok := volumeContext[common.VolumeContextDiskNameKey]
	if !ok {
		return ErrVolumeMissingName
	}

	err := mounter.Mount(diskName, request.GetTargetPath(), fsDiskFilesystem, mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}

func nodePublishSSDFilesystemVolume(
	serialNumber string,
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	_ common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	devicePath := getSSDDevicePath(serialNumber)
	err := mounter.FormatAndMount(devicePath,
		request.GetTargetPath(),
		request.GetVolumeCapability().GetMount().GetFsType(),
		mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, request.GetTargetPath(), err.Error())
	}

	// Resize the filesystem to span the entire disk
	// The size of the underlying disk may have changed due to volume expansion (offline)
	ok, err := resizer.Resize(devicePath, request.GetTargetPath())
	if err != nil {
		return fmt.Errorf("%w at target path %s: %w", ErrFailedResize, request.GetTargetPath(), err)
	}

	if !ok {
		return fmt.Errorf("%w: %s", ErrFailedResize, request.GetTargetPath())
	}

	return nil
}

func nodePublishFilesystemVolume(serialNumber string,
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	diskType common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(request.GetTargetPath(), newDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	mountOpts = append(mountOpts, request.GetVolumeCapability().GetMount().GetMountFlags()...)

	switch diskType {
	case common.DiskTypeFS:
		return nodePublishFSFilesystemVolume(
			serialNumber,
			mounter,
			resizer,
			mountOpts,
			diskType,
			request,
		)
	case common.DiskTypeSSD:
		return nodePublishSSDFilesystemVolume(
			serialNumber,
			mounter,
			resizer,
			mountOpts,
			diskType,
			request,
		)
	default:
		// Switch is intended to be exhaustive, reaching this case is a bug
		panic(fmt.Sprintf(
			"Switch is intended to be exhaustive, %s is not a valid switch case", diskType))
	}

	return nil
}
