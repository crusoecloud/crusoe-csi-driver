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
	nfsRemotePorts string,
	nfsIP string,
	request *csi.NodePublishVolumeRequest,
) error {
	devicePath, err := getFSDevicePath(request, nfsEnabled, nfsIP)
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
			Mounter:        mounter,
			Resizer:        resizer,
			Request:        request,
			DevicePath:     devicePath,
			NFSRemotePorts: nfsRemotePorts,
			NFSIP:          nfsIP,
			MountOpts:      mountOpts,
			NFSEnabled:     nfsEnabled,
		}).Publish()
	default:
		return fmt.Errorf("%w: %s", node.ErrUnexpectedVolumeCapability, request.GetVolumeCapability())
	}
}
