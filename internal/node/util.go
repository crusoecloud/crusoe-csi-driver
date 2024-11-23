package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

const (
	newDirPerms          = 0o755 // this represents: rwxr-xr-x
	newFilePerms         = 0o644 // this represents: rw-r--r--
	expectedTypeSegments = 2
	fsDiskFilesystem     = "virtiofs"
	readOnlyMountOption  = "ro"
)

var (
	errUnexpectedVolumeCapability = errors.New("unexpected volume capability")
	errVolumeMissingSerialNumber  = fmt.Errorf(
		"volume missing serial number context key %s",
		common.VolumeContextDiskSerialNumberKey)
	errVolumeMissingName = fmt.Errorf("volume missing name context key %s", common.VolumeContextDiskNameKey)
	errFailedMount       = errors.New("failed to mount volume")
)

func getSSDDevicePath(serialNumber string) string {
	// symlink: /dev/disk/by-id/virtio-<serial-number>
	return fmt.Sprintf("/dev/disk/by-id/virtio-%s", serialNumber)
}

func nodePublishBlockVolume(devicePath string,
	mounter *mount.SafeFormatAndMount,
	mountOpts []string,
	request *csi.NodePublishVolumeRequest,
) error {
	dirPath := filepath.Dir(request.GetTargetPath())
	// Check if the directory exists
	if _, err := os.Stat(dirPath); errors.Is(err, os.ErrNotExist) {
		// Directory does not exist, create it
		if err := os.MkdirAll(dirPath, newDirPerms); err != nil {
			return fmt.Errorf("failed to make directory for target path: %w", err)
		}
	}

	// expose the block volume as a file
	f, err := os.OpenFile(request.GetTargetPath(), os.O_CREATE|os.O_EXCL, os.FileMode(newFilePerms))
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to make file for target path: %w", err)
		}
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("failed to close file after making target path: %w", err)
	}

	mountOpts = append(mountOpts, "bind")
	mountOpts = append(mountOpts, request.GetVolumeCapability().GetMount().GetMountFlags()...)
	err = mounter.Mount(devicePath, request.GetTargetPath(), "", mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", errFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}

func nodePublishFilesystemVolume(devicePath string,
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	diskType common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	// Check if the directory exists
	if _, err := os.Stat(request.GetTargetPath()); errors.Is(err, os.ErrNotExist) {
		// Directory does not exist, create it
		if mkdirErr := os.MkdirAll(request.GetTargetPath(), newDirPerms); mkdirErr != nil {
			return fmt.Errorf("failed to make directory for target path: %w", mkdirErr)
		}
	}

	mountOpts = append(mountOpts, request.GetVolumeCapability().GetMount().GetMountFlags()...)

	//nolint:nestif // error handling
	if diskType == common.DiskTypeFS {
		volumeContext := request.GetVolumeContext()
		diskName, ok := volumeContext[common.VolumeContextDiskNameKey]
		if !ok {
			return errVolumeMissingName
		}

		err := mounter.Mount(diskName, request.GetTargetPath(), fsDiskFilesystem, mountOpts)
		if err != nil {
			return fmt.Errorf("%w at target path %s: %s", errFailedMount, request.GetTargetPath(), err.Error())
		}
	} else {
		err := mounter.FormatAndMount(devicePath,
			request.GetTargetPath(),
			request.GetVolumeCapability().GetMount().GetFsType(),
			mountOpts)
		if err != nil {
			return fmt.Errorf("%w at target path %s: %s", errFailedMount, request.GetTargetPath(), err.Error())
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
	}

	return nil
}

func nodePublishVolume(mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	diskType common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	volumeContext := request.GetVolumeContext()
	serialNumber, ok := volumeContext[common.VolumeContextDiskSerialNumberKey]
	if !ok {
		return errVolumeMissingSerialNumber
	}

	devicePath := getSSDDevicePath(serialNumber)

	switch {
	case request.GetVolumeCapability().GetBlock() != nil:
		return nodePublishBlockVolume(devicePath, mounter, mountOpts, request)
	case request.GetVolumeCapability().GetMount() != nil:
		return nodePublishFilesystemVolume(devicePath, mounter, resizer, mountOpts, diskType, request)
	default:
		return fmt.Errorf("%w: %s", errUnexpectedVolumeCapability, request.GetVolumeCapability())
	}
}

func supportsFS(node *crusoeapi.InstanceV1Alpha5) bool {
	typeSegments := strings.Split(node.Type_, ".")
	if len(typeSegments) != expectedTypeSegments {
		klog.Infof("Unexpected node type: %s", node.Type_)

		return false
	}

	// All CPU instances support shared filesystems
	if typeSegments[0] == "c1a" || typeSegments[0] == "s1a" {
		return true
	}

	// There are 10 slices in an L40s node
	if typeSegments[0] == "l40s-48gb" && typeSegments[1] == "10x" {
		return true
	}

	// Otherwise, there are 8 slices in every other GPU node
	if typeSegments[1] == "8x" {
		return true
	}

	return false
}
