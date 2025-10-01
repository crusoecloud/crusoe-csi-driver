package ssd

import (
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/mount-utils"
)

func nodePublishVolume(
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	request *csi.NodePublishVolumeRequest,
) error {
	volumeContext := request.GetVolumeContext()
	serialNumber, ok := volumeContext[common.VolumeContextDiskSerialNumberKey]
	if !ok {
		return node.ErrVolumeMissingSerialNumber
	}

	devicePath := getSSDDevicePath(serialNumber)

	alreadyMounted, checkErr := node.VerifyMountedVolumeWithUtils(mounter, request.GetTargetPath(), devicePath)
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
		return (&PublishFilesystem{
			DevicePath: devicePath,
			Mounter:    mounter,
			Resizer:    resizer,
			MountOpts:  mountOpts,
			Request:    request,
		}).Publish()
	default:
		return fmt.Errorf("%w: %s", node.ErrUnexpectedVolumeCapability, request.GetVolumeCapability())
	}
}
