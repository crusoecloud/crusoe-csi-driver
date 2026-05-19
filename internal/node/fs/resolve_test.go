package fs_test

import (
	"errors"
	"net"
	"testing"

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
// These tests cannot run in parallel because they mutate package state
// (the lookupIP function variable inside the fs package).
func withStubLookupIP(t *testing.T, fn func(host string) ([]net.IP, error)) *int {
	t.Helper()

	calls := 0
	prev := fs.SetLookupIP(func(host string) ([]net.IP, error) {
		calls++

		return fn(host)
	})
	t.Cleanup(func() { fs.SetLookupIP(prev) })

	return &calls
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_IPListPassthrough(t *testing.T) {
	calls := withStubLookupIP(t, func(_ string) ([]net.IP, error) {
		t.Fatal("lookupIP should not be invoked when remoteports is already an IP list")

		return nil, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(testIPv4A, testIPv4A+","+testIPv4B)
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

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_DNSResolvesToList(t *testing.T) {
	const lookupHost = "nfs.crusoecloudcompute.com"
	withStubLookupIP(t, func(host string) ([]net.IP, error) {
		if host != lookupHost {
			t.Errorf("unexpected lookup host: %q", host)
		}

		return []net.IP{net.ParseIP(testIPv4A), net.ParseIP(testIPv4B)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(lookupHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A {
		t.Errorf("host = %q, want %q (first IPv4)", gotHost, testIPv4A)
	}
	if gotPorts != testIPv4A+","+testIPv4B {
		t.Errorf("remotePorts = %q, want %q", gotPorts, testIPv4A+","+testIPv4B)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_StripsUnspecifiedV6(t *testing.T) {
	withStubLookupIP(t, func(_ string) ([]net.IP, error) {
		return []net.IP{net.IPv6unspecified, net.ParseIP(testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(testHost, testDNS)
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
	withStubLookupIP(t, func(_ string) ([]net.IP, error) {
		return []net.IP{net.IPv6unspecified, net.IPv4zero}, nil
	})

	_, _, err := fs.MaterializeNFSTarget(testHost, testDNS)
	if err == nil {
		t.Fatal("expected error when all addresses are unspecified, got nil")
	}
	if !errors.Is(err, fs.ErrNoUsableNFSAddressForTest) {
		t.Errorf("expected error to wrap ErrNoUsableNFSAddress, got: %v", err)
	}
}

//nolint:paralleltest // mutates package-level lookupIP via fs.SetLookupIP
func TestMaterializeNFSTarget_V4MappedV6IsKept(t *testing.T) {
	withStubLookupIP(t, func(_ string) ([]net.IP, error) {
		// net.ParseIP("::ffff:1.2.3.4") returns the v4-mapped v6 form; its
		// .To4() returns the unwrapped IPv4, which is what we want to keep.
		return []net.IP{net.ParseIP("::ffff:" + testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(testHost, testDNS)
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
	withStubLookupIP(t, func(_ string) ([]net.IP, error) {
		return nil, errStubLookup
	})

	host, ports, err := fs.MaterializeNFSTarget(testHost, testDNS)
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
	withStubLookupIP(t, func(_ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP(testIPv4A)}, nil
	})

	gotHost, gotPorts, err := fs.MaterializeNFSTarget(testHost, testDNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != testIPv4A || gotPorts != testIPv4A {
		t.Errorf("v6 should be stripped; got host=%q ports=%q", gotHost, gotPorts)
	}
}
