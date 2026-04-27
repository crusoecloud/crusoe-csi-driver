package crusoe_test

import (
	"testing"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
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
			name: "dns name preferred over vips",
			disk: crusoeapi.DiskV1Alpha5{
				DnsName: "nfs.crusoecloudcompute.com",
				Vips:    []string{"1.2.3.4", "1.2.3.8"},
			},
			wantHost:        "nfs.crusoecloudcompute.com",
			wantRemotePorts: "dns",
			wantOK:          true,
		},
		{
			name:            "vip range produces hyphenated remoteports",
			disk:            crusoeapi.DiskV1Alpha5{Vips: []string{"1.2.3.4", "1.2.3.8"}},
			wantHost:        "1.2.3.4",
			wantRemotePorts: "1.2.3.4-1.2.3.8",
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
			name:            "more than two vips uses first and last",
			disk:            crusoeapi.DiskV1Alpha5{Vips: []string{"1.2.3.4", "1.2.3.5", "1.2.3.8"}},
			wantHost:        "1.2.3.4",
			wantRemotePorts: "1.2.3.4-1.2.3.8",
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
