package common

import "time"

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
	SSDPluginName    = "ssd.csi.crusoe.ai"
	SSDPluginVersion = "0.1.0"

	FSPluginName    = "fs.csi.crusoe.ai"
	FSPluginVersion = "0.1.0"
)

// Runtime options.
const (
	// OperationTimeout is the maximum time the Crusoe CSI driver will wait for an asynchronous operation to complete.
	OperationTimeout = 5 * time.Minute

	// MaxVolumesPerNode refers to the maximum number of disks that can be attached to a VM
	// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
	MaxVolumesPerNode = 15

	// MaxDiskNameLength refers to the maximum permissible length of a Crusoe disk name
	// ref: https://docs.crusoecloud.com/storage/disks/overview#persistent-disks
	MaxDiskNameLength = 63
)
