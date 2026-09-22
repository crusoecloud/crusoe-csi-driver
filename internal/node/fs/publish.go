package fs

import (
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

// stageVolume mounts the fs volume once per node at the staging target path if it
// is not already mounted there. The NFS export (or virtiofs shared disk) is mounted
// read-write here; each pod's NodePublishVolume then bind-mounts from this path and
// applies readonly per pod. Both NodeStageVolume and NodePublishVolume drive this
// (via Node.ensureStaged): publish stages too, so a pod still gets a real mount when
// the CO skipped NodeStage, e.g. a driver upgrade where kubelet already treated the
// volume as device-mounted for a pod published before the upgrade (CRUSOE-103809).
// It is idempotent: an already-staged path returns early with no second mount.
func stageVolume(
	mounter *mount.SafeFormatAndMount,
	nfsEnabled bool,
	nfsRemotePorts, nfsHost, stagingTargetPath, volumeID string,
	volumeCapability *csi.VolumeCapability,
	volumeContext map[string]string,
) error {
	if volumeCapability.GetBlock() != nil {
		return fmt.Errorf("%w: %s", node.ErrUnsupportedVolumeCapability, volumeCapability)
	}
	// A nil capability, or one that is neither Block nor Mount, is malformed: reject
	// it instead of falling through to a mount with empty options.
	if volumeCapability.GetMount() == nil {
		return fmt.Errorf("%w: %s", node.ErrUnexpectedVolumeCapability, volumeCapability)
	}

	devicePath, err := getFSDevicePath(volumeID, volumeContext, nfsEnabled, nfsHost)
	if err != nil {
		return fmt.Errorf("failed to get device path: %w", err)
	}

	// Idempotency: if the export is already staged at this path, return early.
	// VerifyMountedVolumeWithUtils is device-name based, which is correct here: the
	// staging path only ever holds the direct export mount, never a bind.
	alreadyMounted, checkErr := node.VerifyMountedVolumeWithUtils(mounter, stagingTargetPath, devicePath)
	if checkErr != nil {
		return fmt.Errorf("failed to verify if volume is already staged: %w", checkErr)
	}
	if alreadyMounted {
		return nil
	}

	return stageMount(mounter, nfsEnabled, nfsRemotePorts, devicePath, stagingTargetPath, volumeCapability.GetMount().GetMountFlags())
}

// stageMount assembles the mount options and performs the real mount at the
// staging path. It is split from stageVolume (which does the device-path lookup and
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
