package ssd

import (
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/mount-utils"
)

type PublishFilesystem struct {
	Mounter    *mount.SafeFormatAndMount
	Resizer    *mount.ResizeFs
	Request    *csi.NodePublishVolumeRequest
	DevicePath string
	MountOpts  []string
}

func (p *PublishFilesystem) Publish() error {
	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(p.Request.GetTargetPath(), node.NewDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	p.MountOpts = append(p.MountOpts, p.Request.GetVolumeCapability().GetMount().GetMountFlags()...)

	err := p.Mounter.FormatAndMount(p.DevicePath,
		p.Request.GetTargetPath(),
		p.Request.GetVolumeCapability().GetMount().GetFsType(),
		p.MountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", node.ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	// Resize the filesystem to span the entire disk
	// The size of the underlying disk may have changed due to volume expansion (offline)
	ok, err := p.Resizer.Resize(p.DevicePath, p.Request.GetTargetPath())
	if err != nil {
		return fmt.Errorf("%w at target path %s: %w", node.ErrFailedResize, p.Request.GetTargetPath(), err)
	}

	if !ok {
		return fmt.Errorf("%w: %s", node.ErrFailedResize, p.Request.GetTargetPath())
	}

	return nil
}
