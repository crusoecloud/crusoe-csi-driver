package node

import (
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"k8s.io/mount-utils"
)

type PublishFilesystem struct {
	DevicePath string
	Mounter    *mount.SafeFormatAndMount
	Resizer    *mount.ResizeFs
	MountOpts  []string
	DiskType   common.DiskType
	NfsEnabled bool
	Request    *csi.NodePublishVolumeRequest
}

func (p PublishFilesystem) Publish() error {
	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(p.Request.GetTargetPath(), newDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	p.MountOpts = append(p.MountOpts, p.Request.GetVolumeCapability().GetMount().GetMountFlags()...)

	switch p.DiskType {
	case common.DiskTypeFS:
		return p.publishFSFilesystemVolume()
	case common.DiskTypeSSD:
		return p.publishSSDFilesystemVolume()
	default:
		// Switch is intended to be exhaustive, reaching this case is a bug
		panic(fmt.Sprintf(
			"Switch is intended to be exhaustive, %s is not a valid switch case", p.DiskType))
	}
}

func (p PublishFilesystem) publishSSDFilesystemVolume() error {
	err := p.Mounter.FormatAndMount(p.DevicePath,
		p.Request.GetTargetPath(),
		p.Request.GetVolumeCapability().GetMount().GetFsType(),
		p.MountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	// Resize the filesystem to span the entire disk
	// The size of the underlying disk may have changed due to volume expansion (offline)
	ok, err := p.Resizer.Resize(p.DevicePath, p.Request.GetTargetPath())
	if err != nil {
		return fmt.Errorf("%w at target path %s: %w", ErrFailedResize, p.Request.GetTargetPath(), err)
	}

	if !ok {
		return fmt.Errorf("%w: %s", ErrFailedResize, p.Request.GetTargetPath())
	}

	return nil
}

func (p PublishFilesystem) publishFSFilesystemVolume() error {
	if p.NfsEnabled {
		publishErr := p.publishNFSFilesystemVolume()
		if publishErr != nil {
			return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, p.Request.GetTargetPath(), publishErr.Error())
		}
	} else {
		publishErr := p.publishVirtiofsFilesystemVolume()
		if publishErr != nil {
			return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, p.Request.GetTargetPath(), publishErr.Error())
		}
	}

	return nil
}

func (p PublishFilesystem) publishVirtiofsFilesystemVolume() error {
	// Mount the disk to the target path
	err := p.Mounter.Mount(p.DevicePath, p.Request.GetTargetPath(), virtioFilesystem, p.MountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	return nil
}

func (p PublishFilesystem) publishNFSFilesystemVolume() error {
	nfsMountOpts := []string{
		"vers=3",
		"nconnect=16",
		"spread_reads",
		"spread_writes",
		fmt.Sprintf("remoteports=%s", nfsStaticRemotePorts),
	}

	p.MountOpts = append(p.MountOpts, nfsMountOpts...)
	nfsDevicePath := getNFSFSDevicePath(p.DevicePath)

	// Mount the disk to the target path
	err := p.Mounter.Mount(nfsDevicePath, p.Request.GetTargetPath(), nfsFilesystem, p.MountOpts)
	if err != nil {
		return fmt.Errorf("%w at target path %s: %s", ErrFailedMount, p.Request.GetTargetPath(), err.Error())
	}

	return nil
}
