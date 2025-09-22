package ssd

import (
	"context"
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

func getSSDDevicePath(serialNumber string) string {
	// symlink: /dev/disk/by-id/virtio-<serial-number>
	return fmt.Sprintf("/dev/disk/by-id/virtio-%s", serialNumber)
}

// NodeExpandVolume This function is currently unused.
// common.DiskTypeFS disks do not require expansion on the node.
// common.DiskTypeSSD disks would require expansion on the node if they supported online expansion.
// nolint
func nodeExpandVolume(ctx context.Context, d *SSDNode, request *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse,
	error,
) {
	// Note that this function will only be called for diskType == common.DiskTypeSSD
	// because FS disks do not require expansion on the node

	// Block devices do not require expansion on the node
	if request.GetVolumeCapability().GetBlock() != nil {
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	// Fetch disk's serial number because NodeExpandVolumeRequest does not include the volume context :(
	disk, err := crusoe.FindDiskByIDFallible(ctx, d.CrusoeClient, d.HostInstance.ProjectId, request.GetVolumeId())
	if err != nil {
		klog.Errorf("failed to find disk %s: %s", request.GetVolumeId(), err)

		return nil, status.Errorf(codes.NotFound, "failed to find disk %s: %s", request.GetVolumeId(), err)
	}
	devicePath := getSSDDevicePath(disk.SerialNumber)

	ok, err := d.Resizer.Resize(devicePath, request.GetVolumePath())
	if err != nil {
		return nil, fmt.Errorf("failed to resize %s: %w", request.GetVolumePath(), err)
	}

	if !ok {
		klog.Errorf("%s for volume %s: %s", node.ErrFailedResize, request.GetVolumePath(), request.GetVolumeId())

		return nil, status.Errorf(codes.Internal, "%s for volume %s: %s",
			node.ErrFailedResize, request.GetVolumePath(), request.GetVolumeId())
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}
