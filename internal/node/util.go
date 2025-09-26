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
	noLoadMountOption    = "noload"
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

func nodePublishBlockVolume(serialNumber string,
	mounter *mount.SafeFormatAndMount,
	mountOpts []string,
	request *csi.NodePublishVolumeRequest,
) error {
	dirPath := filepath.Dir(request.GetTargetPath())
	devicePath := getSSDDevicePath(serialNumber)

	alreadyMounted, verifyErr := VerifyMountedVolumeWithUtils(mounter, request.GetTargetPath(), devicePath)
	if verifyErr != nil {
		return fmt.Errorf("failed to verify mounted volume: %w", verifyErr)
	}

	if alreadyMounted {
		// Correct volume is already mounted, exit early
		return nil
	}

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

	mountOpts = append(mountOpts, "bind")
	mountOpts = append(mountOpts, request.GetVolumeCapability().GetMount().GetMountFlags()...)
	err = mounter.Mount(devicePath, request.GetTargetPath(), "", mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", errFailedMount, request.GetTargetPath(), err.Error())
	}

	return nil
}

//nolint:cyclop // want to keep filesystem logic together
func nodePublishFilesystemVolume(serialNumber string,
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	diskType common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	var devicePath string
	var ok bool
	if diskType == common.DiskTypeFS {
		volumeContext := request.GetVolumeContext()
		devicePath, ok = volumeContext[common.VolumeContextDiskNameKey]
		if !ok {
			return errVolumeMissingName
		}
	} else {
		devicePath = getSSDDevicePath(serialNumber)
	}

	alreadyMounted, verifyErr := VerifyMountedVolumeWithUtils(mounter, request.GetTargetPath(), devicePath)
	if verifyErr != nil {
		return fmt.Errorf("failed to verify mounted volume: %w", verifyErr)
	}

	if alreadyMounted {
		// Correct volume is already mounted, exit early
		return nil
	}

	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(request.GetTargetPath(), newDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	mountOpts = append(mountOpts, request.GetVolumeCapability().GetMount().GetMountFlags()...)

	//nolint:nestif // error handling
	if diskType == common.DiskTypeFS {
		err := mounter.Mount(devicePath, request.GetTargetPath(), fsDiskFilesystem, mountOpts)
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
		resizeOk, err := resizer.Resize(devicePath, request.GetTargetPath())
		if err != nil {
			return fmt.Errorf("%w at target path %s: %w", ErrFailedResize, request.GetTargetPath(), err)
		}

		if !resizeOk {
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

	switch {
	case request.GetVolumeCapability().GetBlock() != nil:
		return nodePublishBlockVolume(serialNumber, mounter, mountOpts, request)
	case request.GetVolumeCapability().GetMount() != nil:
		return nodePublishFilesystemVolume(serialNumber, mounter, resizer, mountOpts, diskType, request)
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

	// There are 4 slices in a GB200 node
	if strings.Contains(typeSegments[0], "gb200-186gb-nvl") && typeSegments[1] == "4x" {
		return true
	}

	// Otherwise, there are 8 slices in every other GPU node
	if typeSegments[1] == "8x" {
		return true
	}

	return false
}
