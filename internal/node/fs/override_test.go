package fs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
)

func TestParseNFSTargetOverride_Valid(t *testing.T) {
	t.Parallel()

	const (
		start = "172.27.1.10"
		end   = "172.27.1.25"
		span  = start + "-" + end
	)

	tests := []struct {
		name            string
		value           string
		wantHost        string
		wantRemotePorts string
	}{
		{name: "empty means no override", value: "", wantHost: "", wantRemotePorts: ""},
		{name: "whitespace means no override", value: "  ", wantHost: "", wantRemotePorts: ""},
		{name: "range", value: span, wantHost: start, wantRemotePorts: span},
		{name: "single ip", value: start, wantHost: start, wantRemotePorts: start},
		{name: "range of one collapses", value: start + "-" + start, wantHost: start, wantRemotePorts: start},
		{name: "surrounding whitespace", value: " " + span + " ", wantHost: start, wantRemotePorts: span},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, remotePorts, err := fs.ParseNFSTargetOverride(tt.value)
			if err != nil {
				t.Fatalf("ParseNFSTargetOverride(%q) returned error: %v", tt.value, err)
			}
			if host != tt.wantHost || remotePorts != tt.wantRemotePorts {
				t.Errorf("ParseNFSTargetOverride(%q) = %q, %q; want %q, %q",
					tt.value, host, remotePorts, tt.wantHost, tt.wantRemotePorts)
			}
		})
	}
}

func TestParseNFSTargetOverride_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "hostname", value: "nfs.crusoecloudcompute.com"},
		{name: "dns sentinel", value: "dns"},
		{name: "missing end", value: "172.27.1.10-"},
		{name: "missing start", value: "-172.27.1.25"},
		{name: "end below start", value: "172.27.1.25-172.27.1.10"},
		{name: "three parts", value: "172.27.1.10-172.27.1.20-172.27.1.25"},
		{name: "comma list", value: "172.27.1.10,172.27.1.11"},
		{name: "ipv6", value: "fd00::1"},
		{name: "unspecified", value: "0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := fs.ParseNFSTargetOverride(tt.value)
			if !errors.Is(err, fs.ErrInvalidNFSTargetOverride) {
				t.Errorf("ParseNFSTargetOverride(%q) error = %v, want ErrInvalidNFSTargetOverride", tt.value, err)
			}
		})
	}
}

// TestResolveNFSTarget_OverrideSkipsDiskAndFlags: with an override set, the
// node returns it without calling the API. CrusoeClient is nil, so a disk
// lookup would panic, and the flag server fails the test if it is hit.
func TestResolveNFSTarget_OverrideSkipsDiskAndFlags(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	node := &fs.Node{
		CrusoeHTTPClient:       srv.Client(),
		CrusoeAPIEndpoint:      srv.URL,
		HostInstance:           &crusoeapi.InstanceV1Alpha5{ProjectId: "proj-1", Location: "some-location"},
		NFSHost:                "10.0.0.2",
		NFSRemotePorts:         "10.0.0.2-10.0.0.9",
		OverrideNFSHost:        "172.27.1.10",
		OverrideNFSRemotePorts: "172.27.1.10-172.27.1.25",
	}

	host, ports := node.ResolveNFSTargetForTest(context.Background(), "vol-1", true)
	if host != "172.27.1.10" || ports != "172.27.1.10-172.27.1.25" {
		t.Errorf("override: host=%q ports=%q, want 172.27.1.10 / 172.27.1.10-172.27.1.25", host, ports)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("API hit %d times with an override set, want 0", n)
	}
}
