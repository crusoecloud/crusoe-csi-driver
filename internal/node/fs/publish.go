package fs

import (
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

// nodeStageVolume mounts the fs volume once per node at the staging target path.
// The NFS export (or virtiofs shared disk) is mounted read-write here; each pod's
// NodePublishVolume then bind-mounts from this path and applies readonly per pod.
// This is the expensive, superblock-creating mount: kubelet issues it once per
// volume per node and reference-counts it, so pod churn no longer re-mounts the
// export.
func nodeStageVolume(
	mounter *mount.SafeFormatAndMount,
	nfsEnabled bool,
	nfsRemotePorts string,
	nfsHost string,
	request *csi.NodeStageVolumeRequest,
) error {
	if request.GetVolumeCapability().GetBlock() != nil {
		return fmt.Errorf("%w: %s", node.ErrUnsupportedVolumeCapability, request.GetVolumeCapability())
	}

	stagingTargetPath := request.GetStagingTargetPath()

	devicePath, err := getFSDevicePath(request.GetVolumeId(), request.GetVolumeContext(), nfsEnabled, nfsHost)
	if err != nil {
		return fmt.Errorf("failed to get device path: %w", err)
	}

	// Idempotency: if the export is already staged at this path, return early.
	alreadyMounted, checkErr := node.VerifyMountedVolumeWithUtils(mounter, stagingTargetPath, devicePath)
	if checkErr != nil {
		return fmt.Errorf("failed to verify if volume is already staged: %w", checkErr)
	}
	if alreadyMounted {
		return nil
	}

	mountFlags := request.GetVolumeCapability().GetMount().GetMountFlags()

	return stageMount(mounter, nfsEnabled, nfsRemotePorts, devicePath, stagingTargetPath, mountFlags)
}

// stageMount assembles the mount options and performs the real mount at the
// staging path. It is split from nodeStageVolume (which does the platform-specific
// already-mounted pre-check) so the option assembly can be unit-tested with a mock
// mounter.
func stageMount(
	mounter *mount.SafeFormatAndMount,
	nfsEnabled bool,
	nfsRemotePorts, devicePath, stagingTargetPath string,
	mountFlags []string,
) error {
	mountOpts := append([]string{}, mountFlags...)

	var filesystem string
	switch {
	case nfsEnabled:
		klog.Infof("Staging NFS volume at %s", stagingTargetPath)
		// Append mandatory NFS mount options.
		mountOpts = append(mountOpts, getNFSMountOpts(nfsRemotePorts)...)
		filesystem = nfsFilesystem
	default:
		klog.Infof("Staging VirtioFS volume at %s", stagingTargetPath)
		filesystem = virtioFilesystem
	}

	return mountFilesystem(mounter, devicePath, stagingTargetPath, filesystem, mountOpts)
}

// nodePublishVolumeBind bind-mounts the already-staged volume from the staging
// target path into the pod's target path. A bind clones the existing mount, so it
// makes no server round-trips and creates no superblock.
func nodePublishVolumeBind(mounter *mount.SafeFormatAndMount, request *csi.NodePublishVolumeRequest) error {
	targetPath := request.GetTargetPath()

	// Idempotency: kubelet re-drives publish on its deadline. If the target is
	// already a mount point it is our bind, so return success. Nothing else mounts
	// at a per-pod CSI target path.
	isMountPoint, err := mounter.IsMountPoint(targetPath)
	switch {
	case err == nil:
		if isMountPoint {
			return nil
		}
	case os.IsNotExist(err):
		// Target does not exist yet; created by bindMount below.
	default:
		return fmt.Errorf("failed to check target path %s: %w", targetPath, err)
	}

	return bindMount(
		mounter,
		request.GetStagingTargetPath(),
		targetPath,
		request.GetReadonly(),
		request.GetVolumeCapability().GetMount().GetMountFlags(),
	)
}

// bindMount binds stagingTargetPath onto targetPath, applying readonly per pod.
// Split from nodePublishVolumeBind (which does the platform-specific mount-point
// pre-check) so it can be unit-tested with a mock mounter.
func bindMount(
	mounter *mount.SafeFormatAndMount,
	stagingTargetPath, targetPath string,
	readonly bool,
	mountFlags []string,
) error {
	mkDirErr := os.MkdirAll(targetPath, node.NewDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	mountOpts := []string{"bind"}
	mountOpts = append(mountOpts, mountFlags...)
	if readonly {
		mountOpts = append(mountOpts, node.ReadOnlyMountOption)
	}

	mountErr := mounter.Mount(stagingTargetPath, targetPath, "", mountOpts)
	if mountErr != nil {
		return fmt.Errorf("%w at target path %s: %s", node.ErrFailedMount, targetPath, mountErr.Error())
	}

	// A bind mount ignores the ro flag on the initial mount, so enforce readonly
	// with a bind remount.
	if readonly {
		remountOpts := []string{"bind", "remount", node.ReadOnlyMountOption}
		remountErr := mounter.Mount(stagingTargetPath, targetPath, "", remountOpts)
		if remountErr != nil {
			return fmt.Errorf("%w (readonly remount) at target path %s: %s",
				node.ErrFailedMount, targetPath, remountErr.Error())
		}
	}

	return nil
}
