package node

import (
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/mount-utils"
	"strings"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"k8s.io/klog/v2"
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
	ErrUnexpectedVolumeCapability = errors.New("unexpected volume capability")
	ErrVolumeMissingSerialNumber  = fmt.Errorf(
		"volume missing serial number context key %s",
		common.VolumeContextDiskSerialNumberKey)
	ErrVolumeMissingName = fmt.Errorf("volume missing name context key %s", common.VolumeContextDiskNameKey)
	ErrFailedMount       = errors.New("failed to mount volume")
)

func getSSDDevicePath(serialNumber string) string {
	// symlink: /dev/disk/by-id/virtio-<serial-number>
	return fmt.Sprintf("/dev/disk/by-id/virtio-%s", serialNumber)
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

func nodePublishVolume(mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	diskType common.DiskType,
	request *csi.NodePublishVolumeRequest,
) error {
	volumeContext := request.GetVolumeContext()
	serialNumber, ok := volumeContext[common.VolumeContextDiskSerialNumberKey]
	if !ok {
		return ErrVolumeMissingSerialNumber
	}

	switch {
	case request.GetVolumeCapability().GetBlock() != nil:
		return nodePublishBlockVolume(serialNumber, mounter, mountOpts, request)
	case request.GetVolumeCapability().GetMount() != nil:
		return nodePublishFilesystemVolume(serialNumber, mounter, resizer, mountOpts, diskType, request)
	default:
		return fmt.Errorf("%w: %s", ErrUnexpectedVolumeCapability, request.GetVolumeCapability())
	}
}
