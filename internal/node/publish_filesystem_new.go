package node

import (
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"k8s.io/mount-utils"
	"net/http"
	"os"
)

type PublishFilesystem struct {
	CrusoeHTTPClient  *http.Client
	CrusoeAPIEndpoint string
	DevicePath        string
	SerialNumber      string
	Mounter           *mount.SafeFormatAndMount
	Resizer           *mount.ResizeFs
	MountOpts         []string
	DiskType          common.DiskType
	Request           *csi.NodePublishVolumeRequest
}

func (p *PublishFilesystem) Publish() error {
	// Make parent directory for target path
	// os.MkdirAll will be a noop if the directory already exists
	mkDirErr := os.MkdirAll(p.Request.GetTargetPath(), newDirPerms)
	if mkDirErr != nil {
		return fmt.Errorf("failed to make directory for target path: %w", mkDirErr)
	}

	p.MountOpts = append(p.MountOpts, p.Request.GetVolumeCapability().GetMount().GetMountFlags()...)

	switch p.DiskType {
	case common.DiskTypeFS:
		return nodePublishFSFilesystemVolume(
			p.DevicePath,
			p.Mounter,
			p.Resizer,
			p.MountOpts,
			p.DiskType,
			p.Request,
		)
	case common.DiskTypeSSD:
		return nodePublishSSDFilesystemVolume(
			p.DevicePath,
			p.Mounter,
			p.Resizer,
			p.MountOpts,
			p.DiskType,
			p.Request,
		)
	default:
		// Switch is intended to be exhaustive, reaching this case is a bug
		panic(fmt.Sprintf(
			"Switch is intended to be exhaustive, %s is not a valid switch case", p.DiskType))
	}
}
