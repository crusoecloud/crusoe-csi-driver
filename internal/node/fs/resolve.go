package fs

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sort"
)

// ErrNoUsableNFSAddress is returned when DNS resolution yields no usable IPv4
// address for an NFS mount target. Callers can use this to distinguish a
// resolver failure from a programmer error.
var ErrNoUsableNFSAddress = errors.New("no usable IPv4 address for NFS target")

// lookupIP is a package-level indirection over net.LookupIP so tests can stub
// out DNS resolution without making real network calls. Production callers
// must not reassign this outside of tests.
//
//nolint:gochecknoglobals // deliberate package-private test seam
var lookupIP = net.LookupIP

// materializeNFSTarget converts a (host, remotePorts) pair into an explicit
// IPv4-only mount target, bypassing the kernel dns_resolver keyring upcall
// that the NFS client would otherwise trigger when remoteports=dns is passed.
//
// If remotePorts is anything other than the literal "dns" (i.e. it is already
// a comma-separated or hyphenated list of IPs), the inputs are returned
// unchanged and no DNS lookup is performed.
//
// If remotePorts == "dns", the hostname is resolved via lookupIP, IPv4-mapped
// IPv6 addresses are normalized to IPv4, unspecified addresses (::, 0.0.0.0)
// and non-IPv4 results are filtered out, and the result is returned as:
//   - newHost: the lowest-valued IPv4, suitable as the host portion of the
//     mount source string ("<ip>:/volumes/<id>"), so that busybox-mount in
//     the Alpine CSI pod never needs to do its own getaddrinfo (closes the
//     INC-450 musl REFUSED surface).
//   - newRemotePorts: kernel-range form "<minIP>-<maxIP>" suitable for the
//     vastnfs remoteports= option, which the kernel expands to every IP in
//     the inclusive range. NOT a comma-separated list: vastnfs rejects
//     comma form with EINVAL (kernel mount returns "Invalid argument").
//     If the DNS response is not contiguous, the range may span IPs that
//     are not actual NFS endpoints; those expand harmlessly because the
//     NFS client only opens connections that the server accepts.
//
// Returns ErrNoUsableNFSAddress (wrapped) when the hostname has no usable
// IPv4 records after filtering. Callers should log the error and fall back
// to legacy "dns" behaviour rather than failing the mount outright so that
// a helper bug never makes the situation worse than before this change.
func materializeNFSTarget(host, remotePorts string) (newHost, newRemotePorts string, err error) {
	if remotePorts != dnsRemotePorts {
		return host, remotePorts, nil
	}

	addrs, err := lookupIP(host)
	if err != nil {
		return host, remotePorts, fmt.Errorf("dns lookup for %q failed: %w", host, err)
	}

	ipv4s := make([]string, 0, len(addrs))
	for _, ip := range addrs {
		if ip.IsUnspecified() {
			continue
		}
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		ipv4s = append(ipv4s, v4.String())
	}

	if len(ipv4s) == 0 {
		return host, remotePorts, fmt.Errorf("%w: %s", ErrNoUsableNFSAddress, host)
	}

	// Sort IPv4 strings by network-byte-order value so the kernel-range form
	// always reflects (lowest, highest). net.ParseIP returns a 16-byte
	// IPv6-mapped form even for IPv4 inputs; ip.To4() returns the canonical
	// 4-byte form for byte-wise comparison.
	sort.Slice(ipv4s, func(i, j int) bool {
		return bytes.Compare(net.ParseIP(ipv4s[i]).To4(), net.ParseIP(ipv4s[j]).To4()) < 0
	})

	first := ipv4s[0]
	last := ipv4s[len(ipv4s)-1]
	if first == last {
		return first, first, nil
	}

	return first, fmt.Sprintf("%s-%s", first, last), nil
}
