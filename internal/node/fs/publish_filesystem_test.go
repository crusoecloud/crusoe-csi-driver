package fs_test

import (
	"errors"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
	"k8s.io/mount-utils"
)

var errMockMount = errors.New("mock mount error")

// mockMounter is a mock implementation of mount.Interface for testing.
type mockMounter struct {
	mount.Interface
	mountError error
	mountCalls []mountCall
}

type mountCall struct {
	source  string
	target  string
	fstype  string
	options []string
}

func (m *mockMounter) Mount(source, target, fstype string, options []string) error {
	m.mountCalls = append(m.mountCalls, mountCall{
		source:  source,
		target:  target,
		fstype:  fstype,
		options: options,
	})

	return m.mountError
}

func (m *mockMounter) MountSensitive(source, target, fstype string, options, sensitiveOptions []string) error {
	return m.Mount(source, target, fstype, append(options, sensitiveOptions...))
}

func (m *mockMounter) Unmount(_ string) error {
	return nil
}

func (m *mockMounter) List() ([]mount.MountPoint, error) {
	return nil, nil
}

func (m *mockMounter) IsLikelyNotMountPoint(_ string) (bool, error) {
	return true, nil
}

func (m *mockMounter) GetMountRefs(_ string) ([]string, error) {
	return nil, nil
}

func TestPublishFilesystem_Publish_NFSVolume(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{
		Interface: mockMnt,
	}

	targetPath := t.TempDir()

	volumeCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				MountFlags: []string{"ro"},
			},
		},
	}

	request := &csi.NodePublishVolumeRequest{
		VolumeId:         "test-volume-id",
		TargetPath:       targetPath,
		VolumeCapability: volumeCapability,
	}

	publisher := &fs.PublishFilesystem{
		Mounter:        mounter,
		Request:        request,
		DevicePath:     "nfs.example.com:/volumes/test-volume-id",
		NFSRemotePorts: "2049-2050",
		MountOpts:      []string{"defaults"},
		NFSEnabled:     true,
	}

	err := publisher.Publish()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(mockMnt.mountCalls) != 1 {
		t.Fatalf("expected 1 mount call, got %d", len(mockMnt.mountCalls))
	}

	call := mockMnt.mountCalls[0]
	if call.source != "nfs.example.com:/volumes/test-volume-id" {
		t.Errorf("expected source 'nfs.example.com:/volumes/test-volume-id', got '%s'", call.source)
	}

	if call.fstype != "nfs" {
		t.Errorf("expected fstype 'nfs', got '%s'", call.fstype)
	}

	// Check that NFS mount options are present
	expectedOptions := []string{
		"defaults", "ro", "vers=3", "nconnect=16", "spread_reads", "spread_writes", "remoteports=2049-2050",
	}
	if len(call.options) != len(expectedOptions) {
		t.Errorf("expected %d mount options, got %d: %v", len(expectedOptions), len(call.options), call.options)
	}

	// Verify all expected options are present
	optionsMap := make(map[string]bool)
	for _, opt := range call.options {
		optionsMap[opt] = true
	}
	for _, expected := range expectedOptions {
		if !optionsMap[expected] {
			t.Errorf("expected mount option '%s' not found in %v", expected, call.options)
		}
	}
}

func TestPublishFilesystem_Publish_NFSVolumeWithDNS(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{
		Interface: mockMnt,
	}

	targetPath := t.TempDir()

	volumeCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				MountFlags: []string{},
			},
		},
	}

	request := &csi.NodePublishVolumeRequest{
		VolumeId:         "test-volume-id",
		TargetPath:       targetPath,
		VolumeCapability: volumeCapability,
	}

	publisher := &fs.PublishFilesystem{
		Mounter:        mounter,
		Request:        request,
		DevicePath:     "nfs.crusoecloudcompute.com:/volumes/test-volume-id",
		NFSRemotePorts: "dns", // DNS for ICAT locations
		MountOpts:      []string{},
		NFSEnabled:     true,
	}

	err := publisher.Publish()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(mockMnt.mountCalls) != 1 {
		t.Fatalf("expected 1 mount call, got %d", len(mockMnt.mountCalls))
	}

	call := mockMnt.mountCalls[0]
	if call.source != "nfs.crusoecloudcompute.com:/volumes/test-volume-id" {
		t.Errorf("expected source 'nfs.crusoecloudcompute.com:/volumes/test-volume-id', got '%s'", call.source)
	}

	if call.fstype != "nfs" {
		t.Errorf("expected fstype 'nfs', got '%s'", call.fstype)
	}

	// Check that NFS mount options include remoteports=dns
	expectedOptions := []string{"vers=3", "nconnect=16", "spread_reads", "spread_writes", "remoteports=dns"}
	if len(call.options) != len(expectedOptions) {
		t.Errorf("expected %d mount options, got %d: %v", len(expectedOptions), len(call.options), call.options)
	}

	// Verify remoteports=dns is present
	foundRemotePorts := false
	for _, opt := range call.options {
		if opt == "remoteports=dns" {
			foundRemotePorts = true

			break
		}
	}
	if !foundRemotePorts {
		t.Errorf("expected remoteports=dns option, got: %v", call.options)
	}
}

func TestPublishFilesystem_Publish_NFSVolumeNoRemotePorts(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{
		Interface: mockMnt,
	}

	targetPath := t.TempDir()

	volumeCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				MountFlags: []string{},
			},
		},
	}

	request := &csi.NodePublishVolumeRequest{
		VolumeId:         "test-volume-id",
		TargetPath:       targetPath,
		VolumeCapability: volumeCapability,
	}

	publisher := &fs.PublishFilesystem{
		Mounter:        mounter,
		Request:        request,
		DevicePath:     "nfs.example.com:/volumes/test-volume-id",
		NFSRemotePorts: "", // Empty - no remoteports option should be added
		MountOpts:      []string{},
		NFSEnabled:     true,
	}

	err := publisher.Publish()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(mockMnt.mountCalls) != 1 {
		t.Fatalf("expected 1 mount call, got %d", len(mockMnt.mountCalls))
	}

	call := mockMnt.mountCalls[0]

	// Check that NFS mount options do NOT include remoteports when empty
	expectedOptions := []string{"vers=3", "nconnect=16", "spread_reads", "spread_writes"}
	if len(call.options) != len(expectedOptions) {
		t.Errorf("expected %d mount options, got %d: %v", len(expectedOptions), len(call.options), call.options)
	}

	// Verify remoteports is not present
	for _, opt := range call.options {
		if len(opt) >= 11 && opt[:11] == "remoteports" {
			t.Errorf("remoteports option should not be present when NFSRemotePorts is empty, got: %v", call.options)
		}
	}
}

func TestPublishFilesystem_Publish_VirtioFSVolume(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{
		Interface: mockMnt,
	}

	targetPath := t.TempDir()

	volumeCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				MountFlags: []string{"rw"},
			},
		},
	}

	request := &csi.NodePublishVolumeRequest{
		VolumeId:         "test-volume-id",
		TargetPath:       targetPath,
		VolumeCapability: volumeCapability,
	}

	publisher := &fs.PublishFilesystem{
		Mounter:    mounter,
		Request:    request,
		DevicePath: "test-disk-name",
		MountOpts:  []string{"defaults"},
		NFSEnabled: false, // VirtioFS
	}

	err := publisher.Publish()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(mockMnt.mountCalls) != 1 {
		t.Fatalf("expected 1 mount call, got %d", len(mockMnt.mountCalls))
	}

	call := mockMnt.mountCalls[0]
	if call.source != "test-disk-name" {
		t.Errorf("expected source 'test-disk-name', got '%s'", call.source)
	}

	if call.fstype != "virtiofs" {
		t.Errorf("expected fstype 'virtiofs', got '%s'", call.fstype)
	}

	// Check that mount options do not include NFS-specific options
	for _, opt := range call.options {
		if opt == "vers=3" || opt == "nconnect=16" {
			t.Errorf("VirtioFS mount should not have NFS options, got: %v", call.options)
		}
	}
}

func TestPublishFilesystem_Publish_MountError(t *testing.T) {
	t.Parallel()

	mockMnt := &mockMounter{
		mountError: errMockMount,
	}
	mounter := &mount.SafeFormatAndMount{
		Interface: mockMnt,
	}

	targetPath := t.TempDir()

	volumeCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				MountFlags: []string{},
			},
		},
	}

	request := &csi.NodePublishVolumeRequest{
		VolumeId:         "test-volume-id",
		TargetPath:       targetPath,
		VolumeCapability: volumeCapability,
	}

	publisher := &fs.PublishFilesystem{
		Mounter:    mounter,
		Request:    request,
		DevicePath: "test-disk",
		MountOpts:  []string{},
		NFSEnabled: false,
	}

	err := publisher.Publish()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, node.ErrFailedMount) {
		t.Errorf("expected error to wrap ErrFailedMount, got: %v", err)
	}
}

func TestPublishFilesystem_Publish_CustomMountOptions(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{
		Interface: mockMnt,
	}

	targetPath := t.TempDir()

	volumeCapability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				MountFlags: []string{"noatime", "nodiratime"},
			},
		},
	}

	request := &csi.NodePublishVolumeRequest{
		VolumeId:         "test-volume-id",
		TargetPath:       targetPath,
		VolumeCapability: volumeCapability,
	}

	publisher := &fs.PublishFilesystem{
		Mounter:    mounter,
		Request:    request,
		DevicePath: "test-disk",
		MountOpts:  []string{"defaults", "ro"},
		NFSEnabled: false,
	}

	err := publisher.Publish()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	call := mockMnt.mountCalls[0]

	// Verify all custom mount options are present
	expectedOpts := map[string]bool{
		"defaults":   false,
		"ro":         false,
		"noatime":    false,
		"nodiratime": false,
	}

	for _, opt := range call.options {
		if _, exists := expectedOpts[opt]; exists {
			expectedOpts[opt] = true
		}
	}

	for opt, found := range expectedOpts {
		if !found {
			t.Errorf("expected mount option '%s' not found in %v", opt, call.options)
		}
	}
}
