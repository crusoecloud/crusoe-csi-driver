package crusoe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/antihax/optional"

	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"k8s.io/klog/v2"
)

// ExpectedVIPRangeLen is the expected length of DiskV1Alpha5.Vips: a
// 2-element [startIP, endIP] range as defined by the storage API contract.
const ExpectedVIPRangeLen = 2

// dnsRemotePortsValue is the literal remoteports= value that makes the vastnfs
// kernel module resolve the hostname via the dns_resolver keyring upcall. The
// userspace-resolution path exists to avoid emitting this; ResolveNFSTarget
// only returns it as the DnsName fallback (and ResolveNFSTargetLegacy as the
// previously-released default).
const dnsRemotePortsValue = "dns"

var (
	ErrUnknownDiskSizeSuffix = errors.New("unknown disk size suffix")

	ErrDiskNotFound           = errors.New("disk not found")
	ErrDiskDifferentSize      = errors.New("disk has different size")
	ErrDiskDifferentName      = errors.New("disk has different name")
	ErrDiskDifferentLocation  = errors.New("disk has different location")
	ErrDiskDifferentBlockSize = errors.New("disk has different block size")
	ErrDiskDifferentType      = errors.New("disk has different type")

	ErrInstanceNotFound  = errors.New("instance not found")
	ErrMultipleInstances = errors.New("multiple instances found")
)

func NormalizeDiskSizeToGiB(disk *crusoeapi.DiskV1Alpha5) (int, error) {
	if strings.HasSuffix(disk.Size, "GiB") {
		sizeGiB, err := strconv.Atoi(strings.TrimSuffix(disk.Size, "GiB"))
		if err != nil {
			return 0, fmt.Errorf("failed to parse disk size: %w", err)
		}

		return sizeGiB, nil
	} else if strings.HasSuffix(disk.Size, "TiB") {
		sizeTiB, err := strconv.Atoi(strings.TrimSuffix(disk.Size, "TiB"))
		if err != nil {
			return 0, fmt.Errorf("failed to parse disk size: %w", err)
		}

		return sizeTiB * common.NumGiBInTiB, nil
	}

	return 0, fmt.Errorf("%w: %s", ErrUnknownDiskSizeSuffix, disk.Size)
}

func FindDiskByNameFallible(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	projectID string,
	name string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx,
		projectID,
		&crusoeapi.DisksApiListDisksOpts{DiskNames: optional.NewInterface([]string{name})})
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", common.UnpackSwaggerErr(listErr))
	}

	if len(disks.Items) != 1 {
		return nil, fmt.Errorf("%w: found %d disks with name %s, expected 1", ErrDiskNotFound, len(disks.Items), name)
	}

	return &disks.Items[0], nil
}

func FindDiskByIDFallible(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	projectID string,
	diskID string,
) (*crusoeapi.DiskV1Alpha5, error) {
	disks, _, listErr := crusoeClient.DisksApi.ListDisks(ctx,
		projectID,
		&crusoeapi.DisksApiListDisksOpts{DiskIds: optional.NewInterface([]string{diskID})})
	if listErr != nil {
		return nil, fmt.Errorf("failed to list disks: %w", common.UnpackSwaggerErr(listErr))
	}

	if len(disks.Items) != 1 {
		return nil, fmt.Errorf("%w: found %d disks with id %s, expected 1", ErrDiskNotFound, len(disks.Items), diskID)
	}

	return &disks.Items[0], nil
}

func GetCreateDiskRequest(request *csi.CreateVolumeRequest,
	location string,
	diskType common.DiskType,
) (*crusoeapi.DisksPostRequestV1Alpha5, error) {
	requestSizeGiB, err := common.RequestSizeToGiB(request.GetCapacityRange())
	if err != nil {
		return nil, fmt.Errorf("failed to parse request size: %w", err)
	}

	var blockSize int64

	if diskType == common.DiskTypeSSD {
		blockSize = common.BlockSizeSSD // TODO: Support different block sizes
	}

	return &crusoeapi.DisksPostRequestV1Alpha5{
		BlockSize: blockSize,
		Location:  location,
		Name:      request.GetName(),
		Size:      fmt.Sprintf("%dGiB", requestSizeGiB),
		Type_:     string(diskType),
	}, nil
}

func CheckDiskMatchesRequest(disk *crusoeapi.DiskV1Alpha5,
	request *csi.CreateVolumeRequest,
	expectedLocation string,
	expectedType common.DiskType,
) error {
	if disk.Name != request.GetName() {
		// This should never happen because we fetch the disk by name
		return ErrDiskDifferentName
	}

	// TODO: Support different block sizes
	if disk.Type_ == string(common.DiskTypeSSD) && disk.BlockSize != common.BlockSizeSSD {
		return ErrDiskDifferentBlockSize
	}

	diskSizeGiB, err := NormalizeDiskSizeToGiB(disk)
	if err != nil {
		return fmt.Errorf("failed to parse disk size: %w", err)
	}

	requestSizeGiB, err := common.RequestSizeToGiB(request.GetCapacityRange())
	if err != nil {
		return fmt.Errorf("failed to parse request size: %w", err)
	}

	if diskSizeGiB != requestSizeGiB {
		return ErrDiskDifferentSize
	}

	if disk.Location != expectedLocation {
		return ErrDiskDifferentLocation
	}

	if disk.Type_ != string(expectedType) {
		return ErrDiskDifferentType
	}

	return nil
}

// ResolveNFSTarget returns the NFS host and remoteports value to use when
// mounting the disk based on the data path connectivity fields populated by
// the storage API. It returns ok=false when the disk carries neither vips nor
// dns_name, signalling that the caller should fall back to a static
// configuration.
//
// Vips is preferred over DnsName: the VIP list is the authoritative set from the
// storage API and lets us mount by explicit IP, bypassing name resolution and
// the dns_resolver keyring upcall (and the ENOKEY / EPROTONOSUPPORT / refused-
// AAAA failure modes that path can hit). When vips is populated we return the
// kernel-range form "<startIP>-<endIP>" so remoteports= expands to every IP in
// the range without invoking the keyring upcall. Only when vips is empty do we
// fall back to dns_name (which the caller will resolve in-process before passing
// to mount).
//
// Vips is contracted to be a 2-element [startIP, endIP] range. We tolerate
// other lengths defensively but warn so the discrepancy is visible. We do NOT
// comma-join the range endpoints — comma-joining would yield only the two
// endpoint IPs and drop every IP in between, defeating the load-balancing the
// range is meant to provide.
//
// Vips are prioritized absolutely when usable: we fall through to DnsName only
// when no usable Vip exists (empty, or every entry filtered out as
// unspecified/non-IPv4), so a stray :: or 0.0.0.0 from the API can never strand
// a mount.
func ResolveNFSTarget(disk *crusoeapi.DiskV1Alpha5) (host, remotePorts string, ok bool) {
	if host, remotePorts, ok := resolveFromVIPs(disk); ok {
		return host, remotePorts, true
	}
	if disk.DnsName != "" {
		return disk.DnsName, dnsRemotePortsValue, true
	}

	return "", "", false
}

// ResolveNFSTargetLegacy is the previously-released resolution order: DnsName
// first (the kernel resolves it via remoteports=dns), then Vips. It is the
// behaviour the CSI driver reproduces when the userspace-DNS feature flag is
// off or the new path fails, keeping FF-off byte-for-byte identical to the
// released driver. Unlike ResolveNFSTarget it does NOT prefer Vips.
func ResolveNFSTargetLegacy(disk *crusoeapi.DiskV1Alpha5) (host, remotePorts string, ok bool) {
	if disk.DnsName != "" {
		return disk.DnsName, dnsRemotePortsValue, true
	}

	return resolveFromVIPs(disk)
}

// resolveFromVIPs builds the kernel-range remoteports string ("startIP-endIP")
// from a disk's vips field. The first IP doubles as the mount-source host so
// busybox-mount in the Alpine CSI pod never has to do its own getaddrinfo
// either. The hyphenated range form is parsed by the Linux NFS client and
// expanded to every IP between start and end inclusive.
//
// Entries are filtered (unspecified/non-IPv4 dropped) and sorted ascending so
// (first, last) is always a valid range; ok=false when nothing usable remains.
func resolveFromVIPs(disk *crusoeapi.DiskV1Alpha5) (host, remotePorts string, ok bool) {
	vips := filterUsableVIPs(disk.Vips)
	switch len(vips) {
	case 0:
		if len(disk.Vips) > 0 {
			klog.Warningf("disk %s: all %d vip(s) were unusable (unspecified/non-IPv4); falling through",
				disk.Id, len(disk.Vips))
		}

		return "", "", false
	case 1:
		return vips[0], vips[0], true
	default:
		if len(vips) != ExpectedVIPRangeLen {
			klog.Warningf("disk %s returned %d usable vips, expected %d ([startIP, endIP]); using min-max range %q-%q",
				disk.Id, len(vips), ExpectedVIPRangeLen, vips[0], vips[len(vips)-1])
		}

		return vips[0], fmt.Sprintf("%s-%s", vips[0], vips[len(vips)-1]), true
	}
}

// filterUsableVIPs drops unspecified (::, 0.0.0.0) and non-parseable entries,
// normalizes IPv4-mapped IPv6 to IPv4, drops non-IPv4 (VAST VIPs are IPv4-only
// at the NFS data plane), and returns the result sorted ascending by network
// byte order so callers can take (first, last) as a kernel range.
func filterUsableVIPs(vips []string) []string {
	usable := make([]string, 0, len(vips))
	for _, v := range vips {
		ip := net.ParseIP(strings.TrimSpace(v))
		if ip == nil || ip.IsUnspecified() {
			continue
		}
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		usable = append(usable, v4.String())
	}
	sort.Slice(usable, func(i, j int) bool {
		return bytes.Compare(net.ParseIP(usable[i]).To4(), net.ParseIP(usable[j]).To4()) < 0
	})

	return usable
}

func GetVolumeFromDisk(disk *crusoeapi.DiskV1Alpha5,

	pluginName,
	location string,
	diskType common.DiskType) (
	*csi.Volume,
	error,
) {
	diskSizeGiB, err := NormalizeDiskSizeToGiB(disk)
	if err != nil {
		return nil, fmt.Errorf("failed to parse disk size: %w", err)
	}

	segments := map[string]string{
		fmt.Sprintf("%s/location", pluginName): location,
	}

	if diskType == common.DiskTypeFS {
		segments[common.GetTopologyKey(pluginName, common.TopologySupportsSharedDisksKey)] = strconv.FormatBool(true)
	}

	return &csi.Volume{
		CapacityBytes: int64(common.NumBytesInGiB) * int64(diskSizeGiB),
		VolumeId:      disk.Id,
		VolumeContext: map[string]string{
			common.VolumeContextDiskSerialNumberKey: disk.SerialNumber,
			common.VolumeContextDiskNameKey:         disk.Name,
		},
		AccessibleTopology: []*csi.Topology{
			{
				Segments: segments,
			},
		},
	}, nil
}

func GetInstanceByID(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	instanceID,
	projectID string,
) (*crusoeapi.InstanceV1Alpha5, error) {
	listVMOpts := &crusoeapi.VMsApiListInstancesOpts{
		Ids: optional.NewString(instanceID),
	}
	instances, _, err := crusoeClient.VMsApi.ListInstances(ctx, projectID, listVMOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances.Items) == 0 {
		return nil, fmt.Errorf("%w: found %d instances with id %s, expected 1",
			ErrInstanceNotFound, len(instances.Items), instanceID)
	} else if len(instances.Items) > 1 {
		return nil, fmt.Errorf("%w: found %d instances with id %s, expected 1",
			ErrMultipleInstances, len(instances.Items), instanceID)
	}

	return &instances.Items[0], nil
}

func CheckDiskAttached(ctx context.Context,
	crusoeClient *crusoeapi.APIClient,
	diskID,
	instanceID,
	projectID string,
) (bool, error) {
	// Use GetInstanceByID (ListInstances) instead of GetInstance because we can easily identify
	// when an instance is not found
	instance, err := GetInstanceByID(ctx, crusoeClient, instanceID, projectID)
	if err != nil {
		return false, fmt.Errorf("failed to get instance: %w", err)
	}

	for i := range instance.Disks {
		if instance.Disks[i].Id == diskID {
			return true, nil
		}
	}

	return false, nil
}
