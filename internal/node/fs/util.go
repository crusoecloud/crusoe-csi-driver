package fs

import (
	"fmt"
	"slices"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/klog/v2"
)

const (
	nfsFilesystem    = "nfs"
	virtioFilesystem = "virtiofs"
)

// cpuInstanceFamilies enumerates the CPU instance-type families that support
// shared filesystems regardless of slice size. Add new CPU shapes here when
// the platform launches them. See the README for the full instance-shape
// support matrix.
var cpuInstanceFamilies = []string{"c1a", "s1a", "c2a", "s2a"}

func getNFSMountOpts(nfsRemotePorts string) []string {
	opts := []string{
		"vers=3",
		"nconnect=16",
		"spread_reads",
		"spread_writes",
	}

	// Only add remoteports if specified
	if nfsRemotePorts != "" {
		opts = append(opts, fmt.Sprintf("remoteports=%s", nfsRemotePorts))
	}

	return opts
}

func supportsFS(instance *crusoeapi.InstanceV1Alpha5) bool {
	typeSegments := strings.Split(instance.Type_, ".")
	if len(typeSegments) != node.ExpectedTypeSegments {
		klog.Infof("Unexpected instance type: %s", instance.Type_)

		return false
	}

	family := typeSegments[0]

	// All supported CPU families support shared filesystems regardless of size.
	if slices.Contains(cpuInstanceFamilies, family) {
		return true
	}

	// L40s instances have 10 slices total; only the full instance is supported.
	if family == "l40s-48gb" && typeSegments[1] == "10x" {
		return true
	}

	// GB200 instances have 4 slices total; only the full instance is supported.
	if strings.HasPrefix(family, "gb200-186gb") && typeSegments[1] == "4x" {
		return true
	}

	// All other GPU families have 8 slices; only the full instance is supported.
	if typeSegments[1] == "8x" {
		return true
	}

	return false
}

func getFSDevicePath(request *csi.NodePublishVolumeRequest, supportsNfs bool, nfsIP string) (string, error) {
	switch {
	case supportsNfs:
		return fmt.Sprintf("%s:/volumes/%s", nfsIP, request.GetVolumeId()), nil
	default:
		volumeContext := request.GetVolumeContext()
		devicePath, ok := volumeContext[common.VolumeContextDiskNameKey]
		if !ok {
			return "", node.ErrVolumeMissingName
		}

		return devicePath, nil
	}
}
