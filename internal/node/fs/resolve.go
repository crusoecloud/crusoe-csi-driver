package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// ErrNoUsableNFSAddress is returned when DNS resolution yields no usable IPv4
// address for an NFS mount target, letting callers distinguish a resolver
// failure from a programmer error.
var ErrNoUsableNFSAddress = errors.New("no usable IPv4 address for NFS target")

// nfsResolveTimeout bounds the in-process DNS resolution for an NFS mount so a
// slow or unresponsive nameserver can never block mount(2); on deadline the
// caller falls back to the default "dns" mount behaviour.
const nfsResolveTimeout = 5 * time.Second

// lookupIP is a package-level indirection over the resolver so tests can stub
// DNS without real network calls. The signature mirrors
// net.Resolver.LookupIP(ctx, network, host); production callers must not
// reassign it outside of tests.
//
// PreferGo explicitly selects the pure-Go resolver. The binary is built with
// CGO_ENABLED=0, so the pure-Go resolver is already the only one compiled in;
// PreferGo is kept as an explicit guard so a future cgo-enabled build can't
// silently fall back to the libc resolver. (AAAA is avoided separately, by the
// "ip4" network argument passed in materializeNFSTarget — not by PreferGo.)
//
//nolint:gochecknoglobals // deliberate package-private test seam
var lookupIP = (&net.Resolver{PreferGo: true}).LookupIP

// materializeNFSTarget converts a (host, remotePorts) pair into an explicit
// IPv4-only mount target, avoiding the kernel dns_resolver keyring upcall that
// the NFS client would otherwise trigger when remoteports=dns is passed.
//
// If remotePorts is anything other than the literal "dns" (i.e. it is already
// an IP list / range), the inputs are returned unchanged and no DNS lookup is
// performed.
//
// If remotePorts == "dns", the hostname is resolved in-process with two
// hardening properties:
//   - IPv4-only: network "ip4" so an AAAA query is never issued, avoiding
//     resolver paths that mishandle a refused or unspecified IPv6 answer.
//   - FQDN: the hostname is given a trailing dot so the resolver skips
//     search-domain (ndots) expansion, removing wasted round-trips and the
//     risk of a search-suffixed name resolving to the wrong record.
//
// The lookup is bounded by nfsResolveTimeout. On any error (lookup failure,
// timeout, or no usable IPv4 after filtering) the inputs are returned unchanged
// and a wrapped error is reported, so the caller can fall back to the default
// "dns" behaviour rather than failing the mount outright.
//
// On success it returns:
//   - newHost: the lowest-valued IPv4, used as the host portion of the mount
//     source string ("<ip>:/volumes/<id>"), so the userspace mount tool does
//     not need its own hostname resolution either.
//   - newRemotePorts: kernel-range form "<minIP>-<maxIP>". A comma-separated
//     list is rejected by the NFS module with EINVAL (observed on the deployed
//     vastnfs version), so only the range form is used. This assumes the NFS
//     endpoint resolves to a CONTIGUOUS IPv4 block (true for the VAST VIP pool):
//     a discontiguous answer would make the range span absent IPs, and the
//     client would pay a TCP connect timeout per absent IP before proceeding. If
//     the endpoint ever resolves to a discontiguous set, switch this to per-IP
//     mounts instead of a range.
func materializeNFSTarget(ctx context.Context, host, remotePorts string) (newHost, newRemotePorts string, err error) {
	if remotePorts != dnsRemotePorts {
		return host, remotePorts, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, nfsResolveTimeout)
	defer cancel()

	// Force FQDN so the resolver skips search-domain expansion.
	fqdn := host
	if !strings.HasSuffix(fqdn, ".") {
		fqdn += "."
	}

	addrs, err := lookupIP(lookupCtx, "ip4", fqdn)
	if err != nil {
		return host, remotePorts, fmt.Errorf("dns lookup for %q failed: %w", fqdn, err)
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

	// Sort by network-byte-order value so the kernel-range form always reflects
	// (lowest, highest). net.IP.To4() gives the canonical 4-byte form for a
	// byte-wise comparison.
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
