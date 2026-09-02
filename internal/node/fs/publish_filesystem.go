package fs

import (
	"fmt"
	"os"

	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/mount-utils"
)

// mountFilesystem mounts devicePath at targetPath with the given filesystem type
// and options, creating the target directory first (a noop if it exists). This is
// the real, superblock-creating mount used by NodeStageVolume; NodePublishVolume
// bind-mounts from the staged path into each pod instead (see nodePublishVolumeBind).
func mountFilesystem(
	mounter *mount.SafeFormatAndMount,
	devicePath, targetPath, filesystem string,
	mountOpts []string,
) error {
	// os.MkdirAll is a noop if the directory already exists.
	mkDirErr := os.MkdirAll(targetPath, node.NewDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	err := mounter.Mount(devicePath, targetPath, filesystem, mountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", node.ErrFailedMount, targetPath, err.Error())
	}

	return nil
}
