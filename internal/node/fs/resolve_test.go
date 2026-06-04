package fs_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
)

const (
	testIPv4A = "1.2.3.4"
	testIPv4B = "5.6.7.8"
	testHost  = "nfs.example.com"
	testDNS   = "dns"
)

var errStubLookup = errors.New("stub lookup error")

// withStubLookupIP swaps the package-level lookupIP for the duration of a
// test. It returns a counter for how many times the stub was invoked so
// tests can assert no DNS lookup happens on the IP-list passthrough path.
//
// The stub signature mirrors net.Resolver.LookupIP(ctx, network, host) so
// tests can assert materializeNFSTarget requests ip4-only and an FQDN host.
//
// These tests cannot run in parallel because they mutate package state
// (the lookupIP function variable inside the fs package).
func withStubLookupIP(
	t *testing.T, fn func(ctx context.Context, network, host string) ([]net.IP, error),
) *int {
	t.Helper()

	calls := 0
	prev := fs.SetLookupIP(func(ctx context.Context, network, host string) ([]net.IP, error) {
		calls++

		return fn(ctx, network, host)
	})
	t.Cleanup(func() { fs.SetLookupIP(prev) })

	return &calls
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_IPListPassthrough(t *testing.T) {
	calls := withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		t.Fatal("lookupIP should not be invoked when remoteports is already an IP list")

		return nil, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), testIPv4A, testIPv4A+","+testIPv4B)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A {
		t.Errorf("host = %q, want %q", gotHost, testIPv4A)
	}
	if gotPorts != testIPv4A+","+testIPv4B {
		t.Errorf("remotePorts = %q, want %q", gotPorts, testIPv4A+","+testIPv4B)
	}
	if *calls != 0 {
		t.Errorf("expected 0 lookupIP calls, got %d", *calls)
	}
}

// TestMaterializeNFSTarget_RequestsIP4AndFQDN is the core new-behavior guard
// (CRUSOE-70481 audit blockers 2 + 3b): the resolver must request ip4-only
// (never AAAA, immune to the OVN ::/REFUSED bug class) and a fully-qualified
// name with a trailing dot (skips ndots:5 search-domain expansion).
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_RequestsIP4AndFQDN(t *testing.T) {
	var gotNetwork, gotHost string
	withStubLookupIP(t, func(_ context.Context, network, host string) ([]net.IP, error) {
		gotNetwork, gotHost = network, host

		return []net.IP{net.ParseIP(testIPv4A)}, nil
	})

	if _, _, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotNetwork != "ip4" {
		t.Errorf("lookup network = %q, want %q (ip4-only: never issue AAAA)", gotNetwork, "ip4")
	}
	if gotHost != testHost+"." {
		t.Errorf("lookup host = %q, want FQDN %q (trailing dot skips search domains)", gotHost, testHost+".")
	}
}

// TestMaterializeNFSTarget_DoesNotDoubleDotFQDN ensures an already-qualified
// name is not given a second trailing dot.
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_DoesNotDoubleDotFQDN(t *testing.T) {
	var gotHost string
	withStubLookupIP(t, func(_ context.Context, _, host string) ([]net.IP, error) {
		gotHost = host

		return []net.IP{net.ParseIP(testIPv4A)}, nil
	})

	if _, _, err := fs.MaterializeNFSTarget(context.Background(), testHost+".", testDNS); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testHost+"." {
		t.Errorf("lookup host = %q, want single trailing dot %q", gotHost, testHost+".")
	}
}

// TestMaterializeNFSTarget_Timeout guards CRUSOE-70481 audit blocker 1: a slow
// or hung resolver must not hang the mount. The bounded timeout must fire and
// the helper must return its inputs unchanged so the caller falls back to the
// legacy "dns" path.
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_Timeout(t *testing.T) {
	withStubLookupIP(t, func(ctx context.Context, _, _ string) ([]net.IP, error) {
		<-ctx.Done() // simulate a hung nameserver

		return nil, ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	host, ports, err := fs.MaterializeNFSTarget(ctx, testHost, testDNS)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if host != testHost || ports != testDNS {
		t.Errorf("on timeout, want inputs returned unchanged; got host=%q ports=%q", host, ports)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_DNSResolvesToRange(t *testing.T) {
	const lookupHost = "nfs.crusoecloudcompute.com"
	withStubLookupIP(t, func(_ context.Context, _, host string) ([]net.IP, error) {
		if host != lookupHost+"." {
			t.Errorf("unexpected lookup host: %q", host)
		}

		return []net.IP{net.ParseIP(testIPv4A), net.ParseIP(testIPv4B)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), lookupHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A {
		t.Errorf("host = %q, want %q (lowest IPv4 by network byte order)", gotHost, testIPv4A)
	}
	// vastnfs rejects comma-separated lists with EINVAL; only the kernel-range
	// form "<low>-<high>" is accepted. See CRUSOE-70481 dlim ICAT test
	// 2026-05-29 for the empirical failure that motivated this assertion.
	want := testIPv4A + "-" + testIPv4B
	if gotPorts != want {
		t.Errorf("remotePorts = %q, want %q (dash-range form, NOT comma-separated)", gotPorts, want)
	}
}

// TestMaterializeNFSTarget_SortsOutOfOrderResponse guards against the kernel
// receiving "<high>-<low>" — the range form requires the lower IP first, and
// net.LookupIP is documented to return addresses in unspecified order. This
// matches the dlim ICAT response we observed (16 VIPs returned in shuffled
// order) which would otherwise produce a malformed range like
// "172.27.255.33-172.27.255.18".
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_SortsOutOfOrderResponse(t *testing.T) {
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		// Deliberately reverse order: DNS hands us testIPv4B (higher) first.
		return []net.IP{net.ParseIP(testIPv4B), net.ParseIP(testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A {
		t.Errorf("host = %q, want %q (must be the lowest IP after sort)", gotHost, testIPv4A)
	}
	want := testIPv4A + "-" + testIPv4B
	if gotPorts != want {
		t.Errorf("remotePorts = %q, want %q (sorted dash-range)", gotPorts, want)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_StripsUnspecifiedV6(t *testing.T) {
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return []net.IP{net.IPv6unspecified, net.ParseIP(testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A {
		t.Errorf("host = %q, want %q", gotHost, testIPv4A)
	}
	if gotPorts != testIPv4A {
		t.Errorf("remotePorts = %q, want %q (:: should be stripped)", gotPorts, testIPv4A)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_OnlyUnspecifiedYieldsError(t *testing.T) {
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return []net.IP{net.IPv6unspecified, net.IPv4zero}, nil
	})

	_, _, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
	if err == nil {
		t.Fatal("expected error when all addresses are unspecified, got nil")
	}
	if !errors.Is(err, fs.ErrNoUsableNFSAddressForTest) {
		t.Errorf("expected error to wrap ErrNoUsableNFSAddress, got: %v", err)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_V4MappedV6IsKept(t *testing.T) {
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		// net.ParseIP("::ffff:1.2.3.4") returns the v4-mapped v6 form; its
		// .To4() returns the unwrapped IPv4, which is what we want to keep.
		return []net.IP{net.ParseIP("::ffff:" + testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A {
		t.Errorf("host = %q, want %q (v4-mapped v6 normalized)", gotHost, testIPv4A)
	}
	if gotPorts != testIPv4A {
		t.Errorf("remotePorts = %q, want %q", gotPorts, testIPv4A)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_LookupError(t *testing.T) {
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return nil, errStubLookup
	})

	host, ports, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
	if err == nil {
		t.Fatal("expected error from lookup failure, got nil")
	}
	if !errors.Is(err, errStubLookup) {
		t.Errorf("expected error to wrap stub error, got: %v", err)
	}
	// Helper should return the inputs unchanged on error so callers can fall
	// back to the legacy "dns" code path rather than failing the mount.
	if host != testHost || ports != testDNS {
		t.Errorf("on error, want inputs returned unchanged; got host=%q ports=%q", host, ports)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_StripsV6Only(t *testing.T) {
	// Belt-and-suspenders: confirm IPv6 (non-mapped) addresses are filtered,
	// since VAST clusters are IPv4-only at the NFS data plane in our deploy.
	withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP(testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A || gotPorts != testIPv4A {
		t.Errorf("v6 should be stripped; got host=%q ports=%q", gotHost, gotPorts)
	}
}

// writeResolvConf writes a resolv.conf fixture and returns its path.
func writeResolvConf(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	return path
}

// TestHostNameservers_ParsesAndFilters guards the host resolv.conf parser that
// backs the CoreDNS-bypass path (CRUSOE-70481): each `nameserver` line becomes
// an "ip:53" dial target in file order; comments, blank lines, search/options
// lines, and malformed IPs are ignored; the systemd stub (127.0.0.53) is kept
// verbatim (dialed from the host netns it reaches host systemd-resolved →
// OVN/CloudDNS).
func TestHostNameservers_ParsesAndFilters(t *testing.T) {
	t.Parallel()

	path := writeResolvConf(t, `# managed by systemd-resolved
; another comment
nameserver 169.254.169.254
search crusoecloudcompute.com svc.cluster.local
options ndots:2
nameserver not-an-ip
nameserver 127.0.0.53
`)

	got, err := fs.HostNameservers(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"169.254.169.254:53", "127.0.0.53:53"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (in-order; malformed dropped; 127.0.0.53 kept)", got, want)
	}
}

// TestHostNameservers_MissingFile ensures a missing host resolv.conf surfaces an
// error (so materializeNFSTarget falls back to the legacy "dns" path rather than
// silently using the pod's CoreDNS resolver).
func TestHostNameservers_MissingFile(t *testing.T) {
	t.Parallel()

	if _, err := fs.HostNameservers(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected error for missing resolv.conf, got nil")
	}
}

// TestHostNameservers_NoNameservers ensures a resolv.conf with no nameserver
// entries is treated as an error rather than yielding an empty dial set.
func TestHostNameservers_NoNameservers(t *testing.T) {
	t.Parallel()

	path := writeResolvConf(t, "search example.com\noptions ndots:2\n# no nameserver lines\n")
	if _, err := fs.HostNameservers(path); err == nil {
		t.Fatal("expected error when no nameserver entries present, got nil")
	}
}
