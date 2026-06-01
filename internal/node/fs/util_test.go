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
		// CPU instances always support shared FS, regardless of slice count.
		{name: "c1a", instanceType: "c1a.16x", want: true},
		{name: "s1a", instanceType: "s1a.8x", want: true},
		{name: "c2a", instanceType: "c2a.4x", want: true},
		{name: "s2a", instanceType: "s2a.2x", want: true},

		// L40s: supported on any slice count (CRUSOE-67560). The full-node
		// .10x case must keep working; smaller slices must now also pass.
		{name: "l40s full node 10x", instanceType: "l40s-48gb.10x", want: true},
		{name: "l40s single slice 1x", instanceType: "l40s-48gb.1x", want: true},
		{name: "l40s partial 2x", instanceType: "l40s-48gb.2x", want: true},
		{name: "l40s partial 5x", instanceType: "l40s-48gb.5x", want: true},

		// GB200: only the full 4x node supports shared FS.
		{name: "gb200 full node 4x", instanceType: "gb200-186gb-nvl.4x", want: true},
		{name: "gb200 partial 1x", instanceType: "gb200-186gb-nvl.1x", want: false},

		// Other GPU SKUs: only the full 8x node supports shared FS.
		{name: "other gpu full node 8x", instanceType: "h100-80gb.8x", want: true},
		{name: "other gpu partial 1x", instanceType: "h100-80gb.1x", want: false},

		// Malformed type strings (not exactly two dot-separated segments).
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
