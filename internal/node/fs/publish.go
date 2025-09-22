package fs

import (
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/mount-utils"
)

func nodePublishVolume(
	mounter *mount.SafeFormatAndMount,
	resizer *mount.ResizeFs,
	mountOpts []string,
	nfsEnabled bool,
	request *csi.NodePublishVolumeRequest,
) error {
	devicePath, err := getFSDevicePath(request, nfsEnabled)
	if err != nil {
		return fmt.Errorf("failed to get device path: %w", err)
	}

	alreadyMounted, checkErr := node.VerifyMountedVolumeWithUtils(mounter, request.GetTargetPath(), devicePath)
	if checkErr != nil {
		return fmt.Errorf("failed to verify if volume is already mounted: %w", checkErr)
	}

	if alreadyMounted {
		return nil
	}

	switch {
	case request.GetVolumeCapability().GetBlock() != nil:
		return fmt.Errorf("%w: %s", node.ErrUnsupportedVolumeCapability, request.GetVolumeCapability())
	case request.GetVolumeCapability().GetMount() != nil:
		return (&PublishFilesystem{
			DevicePath: devicePath,
			Mounter:    mounter,
			Resizer:    resizer,
			MountOpts:  mountOpts,
			NfsEnabled: nfsEnabled,
			Request:    request,
		}).Publish()
	default:
		return fmt.Errorf("%w: %s", node.ErrUnexpectedVolumeCapability, request.GetVolumeCapability())
	}
}
