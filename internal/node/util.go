package node

import (
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/mount-utils"
	"net/http"
	"strings"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"k8s.io/klog/v2"
)

const (
	newDirPerms          = 0o755 // this represents: rwxr-xr-x
	newFilePerms         = 0o644 // this represents: rw-r--r--
	expectedTypeSegments = 2
	nfsFilesystem        = "nfs"
	virtioFilesystem     = "virtiofs"
	readOnlyMountOption  = "ro"
	noLoadMountOption    = "noload"
	nfsStaticRemotePorts = "100.64.0.2-100.64.0.17"
	nfsStaticIP          = "100.64.0.2"
)

var (
	ErrFailedToFetchNFSFlag = errors.New("failed to fetch NFS flag")

	ErrUnexpectedVolumeCapability = errors.New("unexpected volume capability")
	ErrVolumeMissingSerialNumber  = fmt.Errorf(
		"volume missing serial number context key %s",
		common.VolumeContextDiskSerialNumberKey)
	ErrVolumeMissingName = fmt.Errorf("volume missing name context key %s", common.VolumeContextDiskNameKey)
	ErrFailedMount       = errors.New("failed to mount volume")
	ErrFailedResize      = errors.New("failed to resize disk")
)

func getNFSFSDevicePath(fsDevicePath string) string {
	return fmt.Sprintf("%s:/volumes/%s", nfsStaticIP, fsDevicePath)
}

func getFSDevicePath(request *csi.NodePublishVolumeRequest) (string, error) {
	volumeContext := request.GetVolumeContext()
	devicePath, ok := volumeContext[common.VolumeContextDiskNameKey]
	if !ok {
		return "", ErrVolumeMissingName
	}

	return devicePath, nil
}

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

func nodePublishVolume(
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	diskType common.DiskType,
	nfsEnabled bool,
	request *csi.NodePublishVolumeRequest,
) error {
	volumeContext := request.GetVolumeContext()
	serialNumber, ok := volumeContext[common.VolumeContextDiskSerialNumberKey]
	if !ok {
		return ErrVolumeMissingSerialNumber
	}

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

	alreadyMounted, checkErr := verifyMountedVolumeWithUtils(d.Mounter, request.GetTargetPath(), devicePath)
	if checkErr != nil {
		return fmt.Errorf("failed to verify if volume is already mounted: %w", checkErr)
	}

	if alreadyMounted {
		return nil
	}

	switch {
	case request.GetVolumeCapability().GetBlock() != nil:
		return PublishBlock{
			DevicePath: devicePath,
			Mounter:    mounter,
			MountOpts:  mountOpts,
			Request:    request,
		}.Publish()
	case request.GetVolumeCapability().GetMount() != nil:
		return PublishFilesystem{
			DevicePath: devicePath,
			Mounter:    mounter,
			Resizer:    resizer,
			MountOpts:  mountOpts,
			DiskType:   diskType,
			NfsEnabled: nfsEnabled,
			Request:    request,
		}.Publish()
	default:
		return fmt.Errorf("%w: %s", ErrUnexpectedVolumeCapability, request.GetVolumeCapability())
	}
}
