package node

import (
	"errors"
	"fmt"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
)

var (
	ErrFailedToFetchNFSFlag = errors.New("failed to fetch NFS flag")

	ErrUnsupportedVolumeCapability = errors.New("unsupported volume capability")
	ErrUnexpectedVolumeCapability  = errors.New("unexpected volume capability")
	ErrVolumeMissingSerialNumber   = fmt.Errorf(
		"volume missing serial number context key %s",
		common.VolumeContextDiskSerialNumberKey)
	ErrVolumeMissingName = fmt.Errorf("volume missing name context key %s", common.VolumeContextDiskNameKey)
	ErrFailedMount       = errors.New("failed to mount volume")
	ErrFailedResize      = errors.New("failed to resize disk")
)
