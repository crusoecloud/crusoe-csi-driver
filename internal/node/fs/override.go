package fs

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrInvalidNFSTargetOverride is returned when the NFS target override is not
// a single IPv4 address or an ascending "<startIP>-<endIP>" IPv4 range.
var ErrInvalidNFSTargetOverride = errors.New("invalid NFS target override")

// maxOverrideRangeParts is the number of addresses in "<startIP>-<endIP>".
const maxOverrideRangeParts = 2

// ParseNFSTargetOverride turns the NFS target override into the host and
// remoteports values used for every NFS mount on this node, in place of the
// target the disk API returns. An empty value means no override and returns
// empty strings.
//
// The value is either a single IPv4 address or an ascending IPv4 range
// "<startIP>-<endIP>", the same form the disk API's vips produce. The start
// address is the mount source host. Hostnames are rejected: the override exists
// to pin explicit addresses, and a name would bring back the DNS lookup.
func ParseNFSTargetOverride(value string) (host, remotePorts string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", nil
	}

	parts := strings.Split(value, "-")
	if len(parts) > maxOverrideRangeParts {
		return "", "", fmt.Errorf("%w %q: want <IP> or <startIP>-<endIP>", ErrInvalidNFSTargetOverride, value)
	}

	ips := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		ip := net.ParseIP(part).To4()
		if ip == nil || ip.IsUnspecified() {
			return "", "", fmt.Errorf("%w %q: %q is not a usable IPv4 address",
				ErrInvalidNFSTargetOverride, value, part)
		}
		ips = append(ips, ip)
	}

	start, end := ips[0], ips[len(ips)-1]
	switch bytes.Compare(start, end) {
	case 0:
		return start.String(), start.String(), nil
	case 1:
		return "", "", fmt.Errorf("%w %q: end address is below start address", ErrInvalidNFSTargetOverride, value)
	default:
		return start.String(), fmt.Sprintf("%s-%s", start, end), nil
	}
}
