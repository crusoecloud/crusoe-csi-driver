package ssd

import (
	"fmt"
	"os"
	"path/filepath"

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

	// CRUSOE-99112 RED RUN, DO NOT MERGE: bind-mount a directory local to this node instead of
	// the volume, so a write lands on the node and a pod on another node never sees it.
	fake := filepath.Join("/tmp/csi99112-fake", p.Request.GetVolumeId())
	if err := os.MkdirAll(fake, node.NewDirPerms); err != nil {
		return fmt.Errorf("failed to make fake volume directory: %w", err)
	}

	if err := p.Mounter.Mount(fake, p.Request.GetTargetPath(), "", []string{"bind"}); err != nil {
		return fmt.Errorf("%w at target path %s: %s", node.ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	return nil
}
