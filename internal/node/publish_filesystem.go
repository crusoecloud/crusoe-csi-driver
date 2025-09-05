package node

import (
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"k8s.io/mount-utils"
	"os"
)

type PublishFilesystem struct {
}

func nfsFSMountHelper(devicePath string,
	mounter *mount.SafeFormatAndMount,
	mountOpts []string,
	request *csi.NodePublishVolumeRequest) error {

	nfsMountOpts := []string{
		"vers=3",
		"nconnect=16",
		"spread_reads",
		"spread_writes",
		fmt.Sprintf("remoteports=%s", nfsStaticRemotePorts),
	}

	mountOpts = append(mountOpts, nfsMountOpts...)
	nfsDevicePath := getNFSFSDevicePath(devicePath)

	// Mount the disk to the target path
	err := mounter.Mount(nfsDevicePath, request.GetTargetPath(), nfsFilesystem, mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}

func virtiofsFSMountHelper(
	devicePath string,
	mounter *mount.SafeFormatAndMount,
	mountOpts []string,
	request *csi.NodePublishVolumeRequest,
) error {

	// Mount the disk to the target path
	err := mounter.Mount(devicePath, request.GetTargetPath(), virtioFilesystem, mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}

func nodePublishFSFilesystemVolume(
	devicePath string,
	mounter *mount.SafeFormatAndMount,
	_ *mount.ResizeFs,
	mountOpts []string,
	_ common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {

	// TODO: Check if project has NFS flag enabled
	err := virtiofsFSMountHelper(devicePath, mounter, mountOpts, request)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}

func nodePublishSSDFilesystemVolume(
	devicePath string,
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	_ common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
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
	var devicePath string

	switch diskType {
	case common.DiskTypeFS:
		var err error
		devicePath, err = getFSDevicePath(request)
		if err != nil {
			return fmt.Errorf("failed to get device path: %w", err)
		}
	case common.DiskTypeSSD:
		devicePath = getSSDDevicePath(serialNumber)
	}

	// Idempotency check: exit early if the disk is already mounted to the target path
	verifyErr := verifyMountedVolumeWithUtils(mounter, request.GetTargetPath(), devicePath)

	switch {
	// Disk is already mounted to the target path, exit early
	case verifyErr == nil:
		{
			return nil
		}
	// Nothing is mounted at the target path, continue mounting the disk
	case errors.Is(verifyErr, errNothingMounted):
		{
		}
	// Another disk is mounted at the target path, unmount the existing disk and continue mounting the disk
	case errors.Is(verifyErr, errDeviceMismatch):
		{
			unmountErr := mounter.Unmount(request.GetTargetPath())
			if unmountErr != nil {
				return fmt.Errorf("failed to cleanup existing mount at %s", request.GetTargetPath())
			}
		}
	// Failed to verify mounted volume
	default:
		return verifyErr
	}

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
			devicePath,
			mounter,
			resizer,
			mountOpts,
			diskType,
			request,
		)
	case common.DiskTypeSSD:
		return nodePublishSSDFilesystemVolume(
			devicePath,
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
