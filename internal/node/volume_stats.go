package node

import (
	"fmt"
	"io"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
)

// GetFilesystemStats returns volume usage (bytes and inodes) for a filesystem-mounted volume.
func GetFilesystemStats(volumePath string) ([]*csi.VolumeUsage, error) {
	var statfs unix.Statfs_t
	if err := unix.Statfs(volumePath, &statfs); err != nil {
		return nil, fmt.Errorf("statfs on %s: %w", volumePath, err)
	}

	bsize := int64(statfs.Bsize)
	availableBytes := int64(statfs.Bavail) * bsize
	totalBytes := int64(statfs.Blocks) * bsize
	usedBytes := totalBytes - availableBytes

	availableInodes := int64(statfs.Ffree)
	totalInodes := int64(statfs.Files)
	usedInodes := totalInodes - availableInodes

	return []*csi.VolumeUsage{
		{
			Available: availableBytes,
			Total:     totalBytes,
			Used:      usedBytes,
			Unit:      csi.VolumeUsage_BYTES,
		},
		{
			Available: availableInodes,
			Total:     totalInodes,
			Used:      usedInodes,
			Unit:      csi.VolumeUsage_INODES,
		},
	}, nil
}

// GetBlockDeviceStats returns volume usage for a block device (raw volume).
func GetBlockDeviceStats(volumePath string) ([]*csi.VolumeUsage, error) {
	f, err := os.Open(volumePath)
	if err != nil {
		return nil, fmt.Errorf("open block device %s: %w", volumePath, err)
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("seek block device %s: %w", volumePath, err)
	}

	return []*csi.VolumeUsage{
		{
			Total: size,
			Unit:  csi.VolumeUsage_BYTES,
		},
	}, nil
}

// IsBlockDevice returns true if the path is a block device bind-mount target (a regular file),
// as opposed to a filesystem mount (a directory).
func IsBlockDevice(volumePath string) (bool, error) {
	info, err := os.Stat(volumePath)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", volumePath, err)
	}

	return !info.IsDir(), nil
}
