package fs

import (
	"fmt"
	"os"

	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/klog/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/mount-utils"
)

type PublishFilesystem struct {
	Mounter    *mount.SafeFormatAndMount
	Resizer    *mount.ResizeFs
	Request    *csi.NodePublishVolumeRequest
	DevicePath string
	MountOpts  []string
	NfsEnabled bool
}

func (p *PublishFilesystem) Publish() error {
	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(p.Request.GetTargetPath(), node.NewDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	p.MountOpts = append(p.MountOpts, p.Request.GetVolumeCapability().GetMount().GetMountFlags()...)

	mountOpts := p.MountOpts
	var filesystem string

	switch {
	case p.NfsEnabled:
		klog.Infof("Publishing NFS volume")
		// Append mandatory NFS mount options
		mountOpts = append(mountOpts, nfsMountOpts...)
		filesystem = nfsFilesystem
	default:
		klog.Infof("Publishing VirtioFS volume")
		filesystem = virtioFilesystem
	}

	// Mount the disk to the target path
	err := p.Mounter.Mount(p.DevicePath, p.Request.GetTargetPath(), filesystem, mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", node.ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	return nil
}
