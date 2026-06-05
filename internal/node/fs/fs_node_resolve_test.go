package fs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
)

var errResolverDown = errors.New("resolver down")

// flagServer answers the two feature-flag endpoints the fs node consults, and
// counts hits to the secondary-cluster endpoint so a test can assert the legacy
// path is resolved at most once.
func flagServer(t *testing.T, ffUserspace, ffSecondary bool, secondaryHits *int32) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var enabled bool
		switch {
		case strings.HasSuffix(r.URL.Path, "is-using-userspace-dns-resolution"):
			enabled = ffUserspace
		case strings.HasSuffix(r.URL.Path, "is-using-secondary-cluster"):
			atomic.AddInt32(secondaryHits, 1)
			enabled = ffSecondary
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]bool{"enabled": enabled}); err != nil {
			t.Errorf("encode flag response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func newTestNode(srv *httptest.Server, location string) *fs.Node {
	return &fs.Node{
		CrusoeHTTPClient:  srv.Client(),
		CrusoeAPIEndpoint: srv.URL,
		HostInstance:      &crusoeapi.InstanceV1Alpha5{ProjectId: "proj-1", Location: location},
		NFSHost:           "10.0.0.2",
		NFSRemotePorts:    "10.0.0.2-10.0.0.9",
	}
}

// TestResolveNFSTarget_FFOffUsesLegacy: with the flag off, resolution takes the
// legacy path with no materialization — the configured host/ports are returned
// as-is.
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestResolveNFSTarget_FFOffUsesLegacy(t *testing.T) {
	var secondaryHits int32
	node := newTestNode(flagServer(t, false, false, &secondaryHits), "some-other-location")

	host, ports := node.ResolveNFSTargetForTest(context.Background(), "", false)
	if host != "10.0.0.2" || ports != "10.0.0.2-10.0.0.9" {
		t.Errorf("FF-off: host=%q ports=%q, want configured 10.0.0.2 / 10.0.0.2-10.0.0.9", host, ports)
	}
}

// TestResolveNFSTarget_FFOnMaterializeSuccess: with the flag on and no per-disk
// target, the legacy result (a "dns" target here) is materialized to explicit
// IPs.
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestResolveNFSTarget_FFOnMaterializeSuccess(t *testing.T) {
	var secondaryHits int32
	node := newTestNode(flagServer(t, true, true, &secondaryHits), fs.DNSFallbackLocation)
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.2.3.4"), net.ParseIP("1.2.3.8")}, nil
	})

	host, ports := node.ResolveNFSTargetForTest(context.Background(), "", false)
	if host != "1.2.3.4" || ports != "1.2.3.4-1.2.3.8" {
		t.Errorf("FF-on success: host=%q ports=%q, want 1.2.3.4 / 1.2.3.4-1.2.3.8", host, ports)
	}
}

// TestResolveNFSTarget_FFOnMaterializeFailureFallsBackOnce: with the flag on, if
// materialization fails the resolver falls back wholesale to the legacy target.
// The legacy path (and its secondary-cluster flag fetch) must run at most once.
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestResolveNFSTarget_FFOnMaterializeFailureFallsBackOnce(t *testing.T) {
	var secondaryHits int32
	node := newTestNode(flagServer(t, true, true, &secondaryHits), fs.DNSFallbackLocation)
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return nil, errResolverDown
	})

	host, ports := node.ResolveNFSTargetForTest(context.Background(), "", false)
	if host != fs.CrusoeCloudDNSNFSHost || ports != fs.DNSRemotePorts {
		t.Errorf("FF-on fallback: host=%q ports=%q, want legacy %q / %q",
			host, ports, fs.CrusoeCloudDNSNFSHost, fs.DNSRemotePorts)
	}
	if n := atomic.LoadInt32(&secondaryHits); n != 1 {
		t.Errorf("secondary-cluster flag fetched %d times, want exactly 1 (legacy resolved once)", n)
	}
}
