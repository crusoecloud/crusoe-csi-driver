package fs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

// ErrNoUsableNFSAddress is returned when DNS resolution yields no usable IPv4
// address for an NFS mount target. Callers can use this to distinguish a
// resolver failure from a programmer error.
var ErrNoUsableNFSAddress = errors.New("no usable IPv4 address for NFS target")

// errNoHostNameservers is returned when the host resolv.conf has no usable
// nameserver entries; errNoDialTargets is the defensive case of a resolver
// built with an empty server set. Both are static (satisfy err113).
var (
	errNoHostNameservers = errors.New("no nameserver entries in host resolv.conf")
	errNoDialTargets     = errors.New("no host nameservers to dial")
)

// nfsResolveTimeout bounds the in-process DNS resolution for an NFS mount.
// A slow or hung nameserver must never hang mount(2); on deadline the caller
// falls back to the legacy "dns" path (CRUSOE-70481 audit blocker 1).
const nfsResolveTimeout = 5 * time.Second

// hostResolvConfPath is the HOST's resolv.conf as seen from inside the CSI node
// pod. The fs node DaemonSet runs with hostPID:true and the driver process is
// exec'd via `nsenter -t 1 -u -i -n -p` — so it lives in the HOST network
// namespace while keeping the container's mount namespace. /proc/1 is therefore
// host-init and /proc/1/root is the host filesystem root, making this the host's
// effective resolv.conf. Declared as a var so tests can point it at a fixture.
//
//nolint:gochecknoglobals // test seam
var hostResolvConfPath = "/proc/1/root/etc/resolv.conf"

// lookupIP is a package-level indirection over the NFS-target resolver so tests
// can stub DNS without real network calls. Its signature mirrors
// net.Resolver.LookupIP(ctx, network, host). It defaults to hostLookupIP — see
// that function for why we resolve against the HOST's nameservers, not the
// pod's. Production callers must not reassign this outside of tests.
//
//nolint:gochecknoglobals // deliberate package-private test seam
var lookupIP = hostLookupIP

// hostLookupIP resolves an NFS hostname against the HOST's configured
// nameservers instead of the CSI pod's.
//
// Why not the pod's resolver? The pod's resolv.conf points at the in-cluster
// CoreDNS service. In CMK, CoreDNS runs inside the *customer's* cluster — Crusoe
// only seeds it at bootstrap, the customer holds cluster-admin and operates it
// day-2, and it can be misconfigured or overloaded. That is exactly INC-531,
// where a customer CoreDNS pod's DNS-storm pegged the host's OVS. The storage
// mount path must not depend on customer-operated CoreDNS.
//
// The host's nameserver, by contrast, is the VM's own resolver: a query issued
// from the host network namespace egresses the VM vNIC into OVN's DNS intercept,
// which answers nfs.crusoecloudcompute.com directly or forwards to CloudDNS.
// Because the driver already runs in the host netns (nsenter -n), dialing the
// host nameserver IP just works, and nfs.crusoecloudcompute.com is an
// OVN/CloudDNS name (not a *.svc.cluster.local cluster name) so CoreDNS is never
// needed for it. Doing this in code (rather than a pod dnsPolicy/dnsConfig edit)
// keeps the behaviour inside the CSI driver and under the CRUSOE-70481 feature
// flag: on any error here, materializeNFSTarget falls back to the legacy "dns"
// path, which itself resolves via the kernel keyring upcall (host
// systemd-resolved → OVN), also CoreDNS-free.
func hostLookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	servers, err := hostNameservers(hostResolvConfPath)
	if err != nil {
		return nil, fmt.Errorf("resolve via host nameservers: %w", err)
	}

	addrs, err := newHostResolver(servers).LookupIP(ctx, network, host)
	if err != nil {
		return nil, fmt.Errorf("host nameserver lookup for %q: %w", host, err)
	}

	return addrs, nil
}

// hostNameservers parses the `nameserver` entries from a resolv.conf file and
// returns them as "ip:53" dial targets, in file order. A systemd stub entry
// (nameserver 127.0.0.53) is returned as-is and is correct: dialed from the
// host netns it reaches the host's systemd-resolved, which forwards upstream to
// OVN/CloudDNS.
func hostNameservers(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	var servers []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if server, ok := parseNameserverLine(scanner.Text()); ok {
			servers = append(servers, server)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("%w: %s", errNoHostNameservers, path)
	}

	return servers, nil
}

// parseNameserverLine returns the "ip:53" dial target for a resolv.conf
// `nameserver <ip>` line, or ok=false for comments, blanks, other directives
// (search/options/domain), or a malformed IP.
func parseNameserverLine(raw string) (string, bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
		return "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "nameserver" || net.ParseIP(fields[1]) == nil {
		return "", false
	}

	return net.JoinHostPort(fields[1], "53"), true
}

// newHostResolver builds a pure-Go (PreferGo) resolver whose Dial ignores the
// pod resolv.conf nameserver and instead contacts the host's nameservers,
// trying each in order so a dead first server fails over. PreferGo keeps us off
// cgo/musl getaddrinfo (the REFUSED-on-AAAA failure mode from INC-450);
// combined with the ip4-only lookup in materializeNFSTarget this sidesteps the
// OVN AAAA/`::` bug class entirely.
func newHostResolver(servers []string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer

			var lastErr error
			for _, server := range servers {
				conn, dialErr := dialer.DialContext(ctx, network, server)
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			if lastErr == nil {
				lastErr = errNoDialTargets
			}

			return nil, lastErr
		},
	}
}

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
