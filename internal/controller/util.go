package controller

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/container-storage-interface/spec/lib/go/csi"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

var (
	//nolint:gochecknoglobals  // can't construct const map
	ssdAllowedAccessModes = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:        {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:   {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER: {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:  {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:    {},
	}
	//nolint:gochecknoglobals  // can't construct const map
	fsAllowedAccessModes = map[csi.VolumeCapability_AccessMode_Mode]struct{}{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:        {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:   {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:    {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER:  {},
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:   {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER: {},
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:  {},
	}
)

var (
	errNoSizeRequested       = errors.New("no size requested")
	errDiskTooSmall          = errors.New("disk size too small")
	errDiskTooLarge          = errors.New("disk size too large")
	errInvalidDiskSize       = errors.New("invalid disk size")
	errUnsupportedAccessMode = errors.New("access mode not supported")
	errUnsupportedAccessType = errors.New("access type not supported")
)

func supportsAccessMode(volumeCapability *csi.VolumeCapability, diskType common.DiskType) bool {
	switch diskType {
	case common.DiskTypeSSD:
		if _, ok := ssdAllowedAccessModes[volumeCapability.GetAccessMode().GetMode()]; ok {
			return true
		}
	case common.DiskTypeFS:
		if _, ok := fsAllowedAccessModes[volumeCapability.GetAccessMode().GetMode()]; ok {
			return true
		}
	default:
		panic(fmt.Sprintf("unexpected disk type: %s", diskType))
	}

	return false
}

func supportsAccessType(volumeCapability *csi.VolumeCapability, diskType common.DiskType) bool {
	switch diskType {
	case common.DiskTypeSSD:
		return volumeCapability.GetBlock() != nil || volumeCapability.GetMount() != nil
	case common.DiskTypeFS:
		return volumeCapability.GetBlock() == nil && volumeCapability.GetMount() != nil
	default:
		panic(fmt.Sprintf("unexpected disk type: %s", diskType))
	}
}

func supportsCapability(volumeCapability *csi.VolumeCapability, diskType common.DiskType) error {
	supportsMode := supportsAccessMode(volumeCapability, diskType)
	supportsType := supportsAccessType(volumeCapability, diskType)

	if !supportsMode {
		return status.Errorf(
			codes.InvalidArgument,
			"%s: %s",
			errUnsupportedAccessMode,
			volumeCapability.GetAccessMode().GetMode())
	}

	if !supportsType {
		var accessType string

		switch {
		case volumeCapability.GetMount() != nil:
			accessType = "mount"
		case volumeCapability.GetBlock() != nil:
			accessType = "block"
		default:
			accessType = "unknown"
		}

		return status.Errorf(codes.InvalidArgument, "%s: %s", errUnsupportedAccessType, accessType)
	}

	return nil
}

//nolint:gocritic // don't combine parameter types
func getCapacity(diskType common.DiskType) (maxSize int64, minSize int64) {
	switch diskType {
	case common.DiskTypeSSD:
		maxSize = common.MaxSSDSizeGiB * common.NumBytesInGiB
		minSize = common.MinSSDSizeGiB * common.NumBytesInGiB
	case common.DiskTypeFS:
		maxSize = common.MaxFSSizeGiB * common.NumBytesInGiB
		minSize = common.MinFSSizeGiB * common.NumBytesInGiB
	default:
		panic(fmt.Sprintf("unexpected disk type: %s", diskType))
	}

	return maxSize, minSize
}

//nolint:cyclop // not that complex
func validateDiskRequest(request *csi.CreateVolumeRequest, diskType common.DiskType) error {
	capacityRange := request.GetCapacityRange()
	if capacityRange == nil {
		return status.Errorf(codes.InvalidArgument, "%s", errNoSizeRequested)
	}

	requestedSizeBytes, err := common.RequestSizeToBytes(request.GetCapacityRange())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "%s", err)
	}

	maxSize, minSize := getCapacity(diskType)
	if requestedSizeBytes > maxSize {
		return status.Errorf(codes.OutOfRange,
			"%s: maximum size: %d, requested size: %d",
			errDiskTooLarge,
			maxSize,
			requestedSizeBytes)
	}

	if requestedSizeBytes < minSize {
		return status.Errorf(codes.OutOfRange,
			"%s: minimum size: %d, requested size: %d",
			errDiskTooSmall,
			minSize,
			requestedSizeBytes)
	}

	switch diskType {
	case common.DiskTypeSSD:
		if requestedSizeBytes%(common.SSDSizeIncrementGiB*common.NumBytesInGiB) != 0 {
			return status.Errorf(codes.OutOfRange,
				"%s: requested size %d must be a multiple of %d (1GiB)",
				errInvalidDiskSize,
				requestedSizeBytes,
				common.BlockSizeSSD*common.NumBytesInGiB)
		}
	case common.DiskTypeFS:
		if requestedSizeBytes%(common.FSSizeIncrementGiB*common.NumBytesInGiB) != 0 {
			return status.Errorf(codes.OutOfRange,
				"%s: requested size %d must be a multiple of %d (1TiB)",
				errInvalidDiskSize,
				requestedSizeBytes,
				common.NumBytesInGiB)
		}
	}

	for _, capability := range request.GetVolumeCapabilities() {
		capabilityErr := supportsCapability(capability, diskType)
		if capabilityErr != nil {
			return status.Errorf(codes.InvalidArgument, "%s", capabilityErr)
		}
	}

	return nil
}

//nolint:cyclop // not that complex
func parseRequiredTopology(request *csi.CreateVolumeRequest,
	diskType common.DiskType,
	pluginName string,
	hostInstance *crusoeapi.InstanceV1Alpha5) (
	location string,
	requireSupportsFS bool,
) {
	// All provisioned volumes should be accessible from a single topology segment

	switch diskType {
	case common.DiskTypeSSD:
		// If the request is for a persistent disk, we can ignore the "supports-shared-disks" topology key
		// and get the first segment with a location
		var ok bool
		for _, topology := range request.GetAccessibilityRequirements().GetRequisite() {
			if location, ok = topology.Segments[common.GetTopologyKey(pluginName, common.TopologyLocationKey)]; ok {
				return location, requireSupportsFS
			}
		}

		// Otherwise, we default to the location of the controller
		return hostInstance.Location, requireSupportsFS
	case common.DiskTypeFS:
		// If the request is for a shared disk, we require a segment with
		// a location and a "supports-shared-disks" topology key

		//nolint:lll // long names
		for _, topology := range request.GetAccessibilityRequirements().GetRequisite() {
			segmentLocation, locationOk := topology.Segments[common.GetTopologyKey(pluginName, common.TopologyLocationKey)]
			segmentSupportsFS, supportsFSOk := topology.Segments[common.GetTopologyKey(pluginName, common.TopologySupportsSharedDisksKey)]
			segmentSupportsFSBool, parseErr := strconv.ParseBool(segmentSupportsFS)
			if locationOk && supportsFSOk && parseErr == nil && segmentSupportsFSBool {
				return segmentLocation, segmentSupportsFSBool
			}
		}

		// We did not find a topology segment with a location and a "supports-shared-disks" topology key
		return "", false
	default:
		panic(fmt.Sprintf("unexpected disk type: %s", diskType))
	}
}
