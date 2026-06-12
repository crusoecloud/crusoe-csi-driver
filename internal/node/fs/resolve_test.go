package fs_test

import (
	"context"
	"errors"
	"net"
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
	restore := fs.SetLookupIP(func(ctx context.Context, network, host string) ([]net.IP, error) {
		calls++

		return fn(ctx, network, host)
	})
	t.Cleanup(restore)

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

// TestMaterializeNFSTarget_RequestsIP4AndFQDN is the core new-behaviour guard:
// the resolver must request ip4-only (never AAAA, avoiding resolver paths that
// mishandle a refused/unspecified IPv6 answer) and a fully-qualified name with a
// trailing dot (skips search-domain expansion).
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

// TestMaterializeNFSTarget_Timeout: a slow or hung resolver must not hang the
// mount. The bounded timeout must fire and the helper must return its inputs
// unchanged so the caller falls back to the default "dns" path.
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
	// A comma-separated list is rejected by the NFS module with EINVAL; only the
	// kernel-range form "<low>-<high>" is accepted.
	want := testIPv4A + "-" + testIPv4B
	if gotPorts != want {
		t.Errorf("remotePorts = %q, want %q (dash-range form, NOT comma-separated)", gotPorts, want)
	}
}

// TestMaterializeNFSTarget_SortsOutOfOrderResponse guards against the kernel
// receiving "<high>-<low>" — the range form requires the lower IP first, and
// net.LookupIP returns addresses in unspecified order. An unsorted response
// would otherwise produce a malformed range (high endpoint before low).
//
//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_SortsOutOfOrderResponse(t *testing.T) {
	// Cover reversal in each octet (and a 3-element shuffle) so the sort is
	// exercised across the whole address, not just the leading octet.
	cases := []struct {
		name      string
		wantHost  string
		wantPorts string
		in        []string // resolver order (deliberately out of order)
	}{
		{"first octet", testIPv4A, testIPv4A + "-" + testIPv4B, []string{testIPv4B, testIPv4A}},
		{"last octet", "1.1.1.1", "1.1.1.1-1.1.1.2", []string{"1.1.1.2", "1.1.1.1"}},
		{"third octet", "1.1.1.1", "1.1.1.1-1.1.2.1", []string{"1.1.2.1", "1.1.1.1"}},
		{"second octet", "1.1.9.9", "1.1.9.9-1.2.1.1", []string{"1.2.1.1", "1.1.9.9"}},
		{"three shuffled", "1.1.1.1", "1.1.1.1-1.1.1.3", []string{"1.1.1.3", "1.1.1.1", "1.1.1.2"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ips := make([]net.IP, 0, len(tc.in))
			for _, s := range tc.in {
				ips = append(ips, net.ParseIP(s))
			}
			withStubLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
				return ips, nil
			})

			gotHost, gotPorts, err := fs.MaterializeNFSTarget(context.Background(), testHost, testDNS)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotHost != tc.wantHost {
				t.Errorf("host = %q, want %q (lowest IP after sort)", gotHost, tc.wantHost)
			}
			if gotPorts != tc.wantPorts {
				t.Errorf("remotePorts = %q, want %q (sorted dash-range)", gotPorts, tc.wantPorts)
			}
		})
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
	// since the NFS data plane is IPv4-only.
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
