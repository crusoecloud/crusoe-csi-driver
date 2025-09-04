package common

import (
	"fmt"
	"time"
)

// Numeric constants.
const (
	NumBytesInGiB       = 1024 * 1024 * 1024
	NumGiBInTiB         = 1024
	BlockSizeSSD        = 4096
	MinSSDSizeGiB       = 1
	MaxSSDSizeGiB       = NumGiBInTiB * 10
	SSDSizeIncrementGiB = 1
	MinFSSizeGiB        = NumGiBInTiB
	MaxFSSizeGiB        = NumGiBInTiB * 1000
	FSSizeIncrementGiB  = NumGiBInTiB
)

// Map keys.
const (
	TopologyLocationKey            = "location"
	TopologySupportsSharedDisksKey = "supports-shared-disks"

	VolumeContextDiskSerialNumberKey = "csi.crusoe.ai/serial-number"
	VolumeContextDiskNameKey         = "csi.crusoe.ai/disk-name"
)

// Enums.
const (
	// DiskTypeSSD and DiskTypeFS names correspond to the Crusoe API enum values.
	DiskTypeSSD DiskType = "persistent-ssd"
	DiskTypeFS  DiskType = "shared-volume"
)

// Plugin metadata.
const (
	SSDPluginName = "ssd.csi.crusoe.ai"
	FSPluginName  = "fs.csi.crusoe.ai"
)

var (
	//nolint:gochecknoglobals // Need to be a variable to set based on SelectedCSIDriverType at runtime
	PluginName string
	//nolint:gochecknoglobals // Need to be a variable for ldflags injection
	PluginVersion string
	//nolint:gochecknoglobals // Need to be a variable to set based on SelectedCSIDriverType at runtime
	// Technically PluginDiskType will be initialized to an empty string, which is not a valid DiskType
	// However, PluginDiskType will always be overwritten by SetPluginVariables to a valid DiskType.
	PluginDiskType DiskType
)

// Runtime options.
const (
	// OperationTimeout is the maximum time the Crusoe CSI driver will wait for an asynchronous operation to complete.
	OperationTimeout = 5 * time.Minute

	// MaxSSDVolumesPerNode refers to the maximum number of SSD disks that can be attached to a VM,
	// including its boot disk
	// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
	MaxSSDVolumesPerNode = 15

	// MaxFSVolumesPerNode refers to the maximum number of disks that can be attached to a VM
	// ref: https://docs.crusoecloud.com/storage/disks/overview/index.html#shared-disks
	MaxFSVolumesPerNode = 4

	// MaxDiskNameLength refers to the maximum permissible length of a Crusoe disk name
	// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
	MaxDiskNameLength = 63
)

func UserAgent() string {
	return fmt.Sprintf("%s/%s", PluginName, PluginVersion)
}
