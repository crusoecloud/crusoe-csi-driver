//nolint:testpackage // nodeVolumeLimit is unexported; an internal test package is required to exercise it.
package internal

import (
	"testing"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
)

// The platform accepts 15 persistent SSD data disks per VM plus the boot disk
// (cloud-core-gateway maxDataDiskAttachments).
// The fs limit comes from --max-fs-volumes-per-node, which defaults to 64.
func TestNodeVolumeLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		diskType common.DiskType
		fsLimit  int64
		want     int64
		wantErr  bool
	}{
		{name: "ssd", diskType: common.DiskTypeSSD, fsLimit: common.MaxFSVolumesPerNode, want: 15},
		{name: "ssd ignores fs limit", diskType: common.DiskTypeSSD, fsLimit: 0, want: 15},
		{name: "fs default", diskType: common.DiskTypeFS, fsLimit: common.MaxFSVolumesPerNode, want: 64},
		{name: "fs set lower", diskType: common.DiskTypeFS, fsLimit: 16, want: 16},
		{name: "fs zero", diskType: common.DiskTypeFS, fsLimit: 0, wantErr: true},
		{name: "fs negative", diskType: common.DiskTypeFS, fsLimit: -1, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := nodeVolumeLimit(tc.diskType, tc.fsLimit)
			if tc.wantErr {
				if err == nil {
					t.Errorf("nodeVolumeLimit(%q, %d) = %d, want an error", tc.diskType, tc.fsLimit, got)
				}

				return
			}
			if err != nil {
				t.Fatalf("nodeVolumeLimit(%q, %d) returned error: %v", tc.diskType, tc.fsLimit, err)
			}
			if got != tc.want {
				t.Errorf("nodeVolumeLimit(%q, %d) = %d, want %d", tc.diskType, tc.fsLimit, got, tc.want)
			}
		})
	}
}
