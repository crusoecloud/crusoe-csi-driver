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
	nfsFilesystem    = "nfs"
	virtioFilesystem = "virtiofs"
)

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

	// All instance types support shared filesystems over NFS, regardless of SKU
	// or slice count. The per-SKU slice-count restrictions that used to live here
	// were a virtiofs-era constraint: shared disks were backed per-host, so only a
	// full node could share one. Post-NFS-migration there is no host-locality
	// requirement, and region-coordinator remains the enforcement boundary — it
	// only restricts slice counts for projects still on virtiofs (see
	// checkSharedVolumeSliceTypeandNumSlices, gated on
	// IsProjectUsingVirtiofsForSharedDisks). The CSI-side gate was therefore
	// redundant and wrongly blocked sub-full-node slices on NFS projects.
	// CRUSOE-67560.
	return true
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
