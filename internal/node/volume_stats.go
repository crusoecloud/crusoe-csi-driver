package node

import (
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ValidateVolumeStatsRequest checks that the required fields are present.
func ValidateVolumeStatsRequest(req *csi.NodeGetVolumeStatsRequest) error {
	if req.GetVolumeId() == "" {
		return status.Errorf(codes.InvalidArgument, "%s", ErrVolumeIDEmpty)
	}

	if req.GetVolumePath() == "" {
		return status.Errorf(codes.InvalidArgument, "%s", ErrVolumePathEmpty)
	}

	return nil
}

// GetFilesystemVolumeStats returns BYTES and INODES usage for a filesystem-mounted volume.
func GetFilesystemVolumeStats(volumePath string) (*csi.NodeGetVolumeStatsResponse, error) {
	var statfs unix.Statfs_t
	if err := unix.Statfs(volumePath, &statfs); err != nil {
		return nil, status.Errorf(codes.Internal, "%s: %s", ErrStatfs, err)
	}

	bsize := int64(statfs.Bsize) //nolint:unconvert,nolintlint // Bsize is int64 on linux, uint32 on darwin

	//nolint:gosec // filesystem stats will not exceed int64 range
	availableBytes := int64(statfs.Bavail) * bsize
	//nolint:gosec // filesystem stats will not exceed int64 range
	totalBytes := int64(statfs.Blocks) * bsize
	usedBytes := totalBytes - availableBytes

	//nolint:gosec // filesystem stats will not exceed int64 range
	availableInodes := int64(statfs.Ffree)
	//nolint:gosec // filesystem stats will not exceed int64 range
	totalInodes := int64(statfs.Files)
	usedInodes := totalInodes - availableInodes

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
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
		},
		VolumeCondition: &csi.VolumeCondition{
			Abnormal: false,
			Message:  "volume is healthy",
		},
	}, nil
}

// GetVolumeStats validates the request, determines whether the volume path
// is a filesystem mount (directory) or a block device (file), and returns the
// appropriate stats.
func GetVolumeStats(req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	if err := ValidateVolumeStatsRequest(req); err != nil {
		return nil, err
	}

	volumePath := req.GetVolumePath()

	fi, err := os.Stat(volumePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "%s: %s", ErrVolumePathStat, err)
		}

		return nil, status.Errorf(codes.Internal, "%s: %s", ErrVolumePathStat, err)
	}

	// If the path is a directory it is a filesystem-mounted volume.
	if fi.IsDir() {
		return GetFilesystemVolumeStats(volumePath)
	}

	// Non-directory paths (regular files, device nodes) are treated as raw
	// block volumes. The CSI spec does not require usage data for block
	// volumes, so we return a healthy condition with no usage entries.
	return &csi.NodeGetVolumeStatsResponse{
		VolumeCondition: &csi.VolumeCondition{
			Abnormal: false,
			Message:  "volume is healthy",
		},
	}, nil
}
