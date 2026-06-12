package crusoe_test

import (
	"testing"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
)

const (
	testVIPHost  = "1.2.3.4"
	testVIPRange = "1.2.3.4-1.2.3.8"
)

func TestResolveNFSTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		wantHost        string
		wantRemotePorts string
		disk            crusoeapi.DiskV1Alpha5
		wantOK          bool
	}{
		{
			name:            "empty disk falls through",
			disk:            crusoeapi.DiskV1Alpha5{},
			wantHost:        "",
			wantRemotePorts: "",
			wantOK:          false,
		},
		{
			name:            "dns name only",
			disk:            crusoeapi.DiskV1Alpha5{DnsName: "nfs.crusoecloudcompute.com"},
			wantHost:        "nfs.crusoecloudcompute.com",
			wantRemotePorts: "dns",
			wantOK:          true,
		},
		{
			name: "vips preferred over dns name",
			disk: crusoeapi.DiskV1Alpha5{
				DnsName: "nfs.crusoecloudcompute.com",
				Vips:    []string{testVIPHost, "1.2.3.8"},
			},
			wantHost:        testVIPHost,
			wantRemotePorts: testVIPRange,
			wantOK:          true,
		},
		{
			name:            "vip range produces kernel-range remoteports",
			disk:            crusoeapi.DiskV1Alpha5{Vips: []string{testVIPHost, "1.2.3.8"}},
			wantHost:        testVIPHost,
			wantRemotePorts: testVIPRange,
			wantOK:          true,
		},
		{
			name:            "single vip used as both host and remoteports",
			disk:            crusoeapi.DiskV1Alpha5{Vips: []string{"100.64.0.2"}},
			wantHost:        "100.64.0.2",
			wantRemotePorts: "100.64.0.2",
			wantOK:          true,
		},
		{
			name:            "more than two vips uses first and last as range endpoints",
			disk:            crusoeapi.DiskV1Alpha5{Vips: []string{testVIPHost, "1.2.3.5", "1.2.3.8"}},
			wantHost:        testVIPHost,
			wantRemotePorts: testVIPRange,
			wantOK:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotHost, gotRemotePorts, gotOK := crusoe.ResolveNFSTarget(&tt.disk)
			if gotHost != tt.wantHost {
				t.Errorf("host = %q, want %q", gotHost, tt.wantHost)
			}
			if gotRemotePorts != tt.wantRemotePorts {
				t.Errorf("remotePorts = %q, want %q", gotRemotePorts, tt.wantRemotePorts)
			}
			if gotOK != tt.wantOK {
				t.Errorf("ok = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

// TestResolveNFSTarget_FiltersUnspecifiedVIPs guards the Vips
// safety filter: unspecified / non-IPv4 entries are dropped before the
// kernel-range is computed, so a stray :: or 0.0.0.0 from the API can never be
// stamped into the mount command.
func TestResolveNFSTarget_FiltersUnspecifiedVIPs(t *testing.T) {
	t.Parallel()

	disk := crusoeapi.DiskV1Alpha5{Vips: []string{"::", testVIPHost, "0.0.0.0", "1.2.3.8"}}
	host, remotePorts, ok := crusoe.ResolveNFSTarget(&disk)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if host != testVIPHost {
		t.Errorf("host = %q, want %q", host, testVIPHost)
	}
	if remotePorts != testVIPRange {
		t.Errorf("remotePorts = %q, want %q (unspecified entries filtered)", remotePorts, testVIPRange)
	}
}

// TestResolveNFSTarget_AllUnusableVIPsFallToDNS ensures that when every Vip is
// unusable we do NOT fail — we fall through to DnsName so the mount can still
// proceed via the legacy path. "Prioritize Vips absolutely" means absolutely
// when they are usable, not when they are garbage.
func TestResolveNFSTarget_AllUnusableVIPsFallToDNS(t *testing.T) {
	t.Parallel()

	disk := crusoeapi.DiskV1Alpha5{
		Vips:    []string{"::", "0.0.0.0"},
		DnsName: "nfs.crusoecloudcompute.com",
	}
	host, remotePorts, ok := crusoe.ResolveNFSTarget(&disk)
	if !ok {
		t.Fatal("ok = false, want true (should fall through to DnsName)")
	}
	if host != "nfs.crusoecloudcompute.com" || remotePorts != "dns" {
		t.Errorf("host=%q remotePorts=%q, want DnsName/\"dns\"", host, remotePorts)
	}
}

// TestResolveNFSTargetLegacy_PrefersDnsName pins the previously-released ordering
// (DnsName first) that the FF-off / fallback path must reproduce byte-for-byte:
// when both DnsName and Vips are present, legacy resolves to DnsName/"dns",
// whereas the new ResolveNFSTarget prefers Vips.
func TestResolveNFSTargetLegacy_PrefersDnsName(t *testing.T) {
	t.Parallel()

	disk := crusoeapi.DiskV1Alpha5{
		DnsName: "nfs.crusoecloudcompute.com",
		Vips:    []string{testVIPHost, "1.2.3.8"},
	}

	legacyHost, legacyPorts, ok := crusoe.ResolveNFSTargetLegacy(&disk)
	if !ok {
		t.Fatal("legacy ok = false, want true")
	}
	if legacyHost != "nfs.crusoecloudcompute.com" || legacyPorts != "dns" {
		t.Errorf("legacy host=%q ports=%q, want DnsName/\"dns\" (DnsName-first)", legacyHost, legacyPorts)
	}

	// Contrast: the new (Vips-first) resolver must pick Vips for the same disk.
	newHost, newPorts, _ := crusoe.ResolveNFSTarget(&disk)
	if newHost != testVIPHost || newPorts != testVIPRange {
		t.Errorf("new host=%q ports=%q, want Vips range (Vips-first)", newHost, newPorts)
	}
}

func TestResolveNFSTargetLegacy_VipsWhenNoDnsName(t *testing.T) {
	t.Parallel()

	disk := crusoeapi.DiskV1Alpha5{Vips: []string{testVIPHost, "1.2.3.8"}}
	host, remotePorts, ok := crusoe.ResolveNFSTargetLegacy(&disk)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if host != testVIPHost || remotePorts != testVIPRange {
		t.Errorf("host=%q remotePorts=%q, want Vips range", host, remotePorts)
	}
}

func TestResolveNFSTargetLegacy_EmptyFallsThrough(t *testing.T) {
	t.Parallel()

	if _, _, ok := crusoe.ResolveNFSTargetLegacy(&crusoeapi.DiskV1Alpha5{}); ok {
		t.Error("ok = true, want false for empty disk")
	}
}
