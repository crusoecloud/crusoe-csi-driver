package fs

import (
	"fmt"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"k8s.io/klog/v2"
)

const (
	nfsFilesystem        = "nfs"
	virtioFilesystem     = "virtiofs"
	nfsStaticRemotePorts = "204.52.31.176-204.52.31.191"
	nfsStaticIP          = "204.52.31.176"
)

//nolint:gochecknoglobals // can't construct const slice
var nfsMountOpts = []string{
	"vers=3",
	"nconnect=16",
	"spread_reads",
	"spread_writes",
	fmt.Sprintf("remoteports=%s", nfsStaticRemotePorts),
}

func supportsFS(instance *crusoeapi.InstanceV1Alpha5) bool {
	typeSegments := strings.Split(instance.Type_, ".")
	if len(typeSegments) != node.ExpectedTypeSegments {
		klog.Infof("Unexpected instance type: %s", instance.Type_)

		return false
	}

	// All CPU instances support shared filesystems
	if typeSegments[0] == "c1a" || typeSegments[0] == "s1a" {
		return true
	}

	// There are 10 slices in an L40s instance
	if typeSegments[0] == "l40s-48gb" && typeSegments[1] == "10x" {
		return true
	}

	// There are 4 slices in a GB200 instance
	if typeSegments[0] == "gb200-186gb-nvl" && typeSegments[1] == "4x" {
		return true
	}

	// Otherwise, there are 8 slices in every other GPU instance
	if typeSegments[1] == "8x" {
		return true
	}

	return false
}

func getFSDevicePath(request *csi.NodePublishVolumeRequest, supportsNfs bool) (string, error) {
	switch {
	case supportsNfs:
		return fmt.Sprintf("%s:/volumes/%s", nfsStaticIP, request.GetVolumeId()), nil
	default:
		volumeContext := request.GetVolumeContext()
		devicePath, ok := volumeContext[common.VolumeContextDiskNameKey]
		if !ok {
			return "", node.ErrVolumeMissingName
		}

		return devicePath, nil
	}
}
