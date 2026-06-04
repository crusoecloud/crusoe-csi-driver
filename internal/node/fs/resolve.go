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
// address for an NFS mount target. Callers can use this to distinguish a
// resolver failure from a programmer error.
var ErrNoUsableNFSAddress = errors.New("no usable IPv4 address for NFS target")

// nfsResolveTimeout bounds the in-process DNS resolution for an NFS mount.
// A slow or hung nameserver must never hang mount(2); on deadline the caller
// falls back to the legacy "dns" path (CRUSOE-70481 audit blocker 1).
const nfsResolveTimeout = 5 * time.Second

// lookupIP is a package-level indirection over a pure-Go resolver so tests can
// stub out DNS resolution without making real network calls. PreferGo keeps us
// off cgo/musl getaddrinfo (which has its own REFUSED-on-AAAA failure mode, see
// INC-450); the signature mirrors net.Resolver.LookupIP(ctx, network, host).
// Production callers must not reassign this outside of tests.
//
//nolint:gochecknoglobals // deliberate package-private test seam
var lookupIP = (&net.Resolver{PreferGo: true}).LookupIP

// materializeNFSTarget converts a (host, remotePorts) pair into an explicit
// IPv4-only mount target, bypassing the kernel dns_resolver keyring upcall
// that the NFS client would otherwise trigger when remoteports=dns is passed.
//
// If remotePorts is anything other than the literal "dns" (i.e. it is already
// an IP list / range), the inputs are returned unchanged and no DNS lookup is
// performed.
//
// If remotePorts == "dns", the hostname is resolved in-process. Two hardening
// properties matter (CRUSOE-70481 audit):
//   - IPv4-only: we request network "ip4" so an AAAA query is never issued.
//     This makes us immune to the OVN ::/REFUSED AAAA bug class (INC-450,
//     INC-483) on any cluster, regardless of the CloudDNS migration state.
//   - FQDN: the hostname is given a trailing dot so the resolver skips
//     ndots:5 search-domain expansion (the CSI pod's resolv.conf has ndots:5
//     plus cluster search domains), removing wasted round-trips and the risk
//     of a search-suffixed name resolving to the wrong record.
//
// The lookup is bounded by nfsResolveTimeout. On any error (lookup failure,
// timeout, or no usable IPv4 after filtering) the inputs are returned
// unchanged and a wrapped error is reported, so the caller can fall back to
// the legacy "dns" behaviour rather than failing the mount outright.
//
// On success it returns:
//   - newHost: the lowest-valued IPv4, used as the host portion of the mount
//     source string ("<ip>:/volumes/<id>"), so busybox-mount in the Alpine CSI
//     pod never needs its own getaddrinfo either (closes the INC-450 surface).
//   - newRemotePorts: kernel-range form "<minIP>-<maxIP>". NOT a comma list:
//     vastnfs rejects comma form with EINVAL (kernel mount "Invalid argument";
//     observed on dlim ICAT 2026-05-29). If the response is non-contiguous the
//     range may span IPs that are not endpoints; those expand harmlessly
//     because the NFS client only opens connections the server accepts.
func materializeNFSTarget(ctx context.Context, host, remotePorts string) (newHost, newRemotePorts string, err error) {
	if remotePorts != dnsRemotePorts {
		return host, remotePorts, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, nfsResolveTimeout)
	defer cancel()

	// Force FQDN so the resolver skips ndots search-domain expansion.
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
