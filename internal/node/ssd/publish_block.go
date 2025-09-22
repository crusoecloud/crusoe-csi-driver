package ssd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/mount-utils"
)

type PublishBlock struct {
	Mounter    *mount.SafeFormatAndMount
	Request    *csi.NodePublishVolumeRequest
	DevicePath string
	MountOpts  []string
}

func (p PublishBlock) Publish() error {
	dirPath := filepath.Dir(p.Request.GetTargetPath())

	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(dirPath, node.NewDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	// Check if the block volume file exists
	_, err := os.Stat(p.Request.GetTargetPath())
	if errors.Is(err, os.ErrNotExist) {
		// Expose the block volume as a file
		f, openErr := os.OpenFile(p.Request.GetTargetPath(), os.O_CREATE|os.O_EXCL, os.FileMode(node.NewFilePerms))
		if openErr != nil {
			return fmt.Errorf("failed to make file for target path: %w", err)
		}
		if err = f.Close(); err != nil {
			return fmt.Errorf("failed to close file after making target path: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check if target path exists: %w", err)
	}

	p.MountOpts = append(p.MountOpts, "bind")

	err = p.Mounter.Mount(p.DevicePath, p.Request.GetTargetPath(), "", p.MountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", node.ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	return nil
}
