package fs

import (
	"testing"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

func TestSupportsFS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		instanceType string
		want         bool
	}{
		// Any well-formed instance type supports shared FS over NFS, regardless
		// of SKU or slice count. region-coordinator remains the enforcement
		// boundary for virtiofs projects (CRUSOE-67560).

		// CPU instances.
		{name: "c1a", instanceType: "c1a.16x", want: true},
		{name: "s1a", instanceType: "s1a.8x", want: true},
		{name: "c2a", instanceType: "c2a.4x", want: true},
		{name: "s2a", instanceType: "s2a.2x", want: true},

		// L40s — any slice count, including sub-full-node (the CRUSOE-67560 case).
		{name: "l40s full node 10x", instanceType: "l40s-48gb.10x", want: true},
		{name: "l40s single slice 1x", instanceType: "l40s-48gb.1x", want: true},
		{name: "l40s partial 5x", instanceType: "l40s-48gb.5x", want: true},

		// GB200 — full node and partial both supported now.
		{name: "gb200 full node 4x", instanceType: "gb200-186gb-nvl.4x", want: true},
		{name: "gb200 partial 1x", instanceType: "gb200-186gb-nvl.1x", want: true},

		// Other GPU SKUs — full node and partial both supported now.
		{name: "other gpu full node 8x", instanceType: "h100-80gb.8x", want: true},
		{name: "other gpu partial 1x", instanceType: "h100-80gb.1x", want: true},

		// Malformed type strings (not exactly two dot-separated segments) are the
		// only inputs that are rejected.
		{name: "missing slice segment", instanceType: "l40s-48gb", want: false},
		{name: "too many segments", instanceType: "l40s-48gb.10x.foo", want: false},
		{name: "empty", instanceType: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			instance := &crusoeapi.InstanceV1Alpha5{Type_: tc.instanceType}
			if got := supportsFS(instance); got != tc.want {
				t.Errorf("supportsFS(%q) = %v, want %v", tc.instanceType, got, tc.want)
			}
		})
	}
}
