//nolint:testpackage // nodeVolumeLimit is unexported; an internal test package is required to exercise it.
package internal

import (
	"testing"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
)

// The platform accepts 15 persistent SSD data disks per VM plus the boot disk
// (cloud-core-gateway maxDataDiskAttachments, CRUSOE-118153).
func TestNodeVolumeLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		diskType common.DiskType
		want     int64
	}{
		{diskType: common.DiskTypeSSD, want: 15},
		{diskType: common.DiskTypeFS, want: 4},
	}

	for _, tc := range cases {
		t.Run(string(tc.diskType), func(t *testing.T) {
			t.Parallel()

			if got := nodeVolumeLimit(tc.diskType); got != tc.want {
				t.Errorf("nodeVolumeLimit(%q) = %d, want %d", tc.diskType, got, tc.want)
			}
		})
	}
}
