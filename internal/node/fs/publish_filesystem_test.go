package fs_test

import (
	"errors"
	"testing"

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

func TestStageMount_NFSVolume(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	staging := t.TempDir()
	err := fs.StageMountForTest(
		mounter, true, "2049-2050", "nfs.example.com:/volumes/test-volume-id", staging, []string{"defaults", "ro"})
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
	if call.target != staging {
		t.Errorf("expected target to be the staging path %q, got %q", staging, call.target)
	}
	if call.fstype != "nfs" {
		t.Errorf("expected fstype 'nfs', got '%s'", call.fstype)
	}

	expectedOptions := []string{
		"defaults", "ro", "vers=3", "nconnect=16", "spread_reads", "spread_writes", "remoteports=2049-2050",
	}
	assertOptionsEqual(t, expectedOptions, call.options)
}

func TestStageMount_NFSVolumeWithDNS(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	err := fs.StageMountForTest(
		mounter, true, "dns", "nfs.crusoecloudcompute.com:/volumes/test-volume-id", t.TempDir(), []string{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	call := mockMnt.mountCalls[0]
	if call.source != "nfs.crusoecloudcompute.com:/volumes/test-volume-id" {
		t.Errorf("expected source 'nfs.crusoecloudcompute.com:/volumes/test-volume-id', got '%s'", call.source)
	}
	if call.fstype != "nfs" {
		t.Errorf("expected fstype 'nfs', got '%s'", call.fstype)
	}

	expectedOptions := []string{"vers=3", "nconnect=16", "spread_reads", "spread_writes", "remoteports=dns"}
	assertOptionsEqual(t, expectedOptions, call.options)
}

func TestStageMount_NFSVolumeNoRemotePorts(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	err := fs.StageMountForTest(
		mounter, true, "", "nfs.example.com:/volumes/test-volume-id", t.TempDir(), []string{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	call := mockMnt.mountCalls[0]
	expectedOptions := []string{"vers=3", "nconnect=16", "spread_reads", "spread_writes"}
	assertOptionsEqual(t, expectedOptions, call.options)

	for _, opt := range call.options {
		if len(opt) >= 11 && opt[:11] == "remoteports" {
			t.Errorf("remoteports option should not be present when empty, got: %v", call.options)
		}
	}
}

func TestStageMount_VirtioFSVolume(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	err := fs.StageMountForTest(mounter, false, "", "test-disk-name", t.TempDir(), []string{"defaults", "rw"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	call := mockMnt.mountCalls[0]
	if call.source != "test-disk-name" {
		t.Errorf("expected source 'test-disk-name', got '%s'", call.source)
	}
	if call.fstype != "virtiofs" {
		t.Errorf("expected fstype 'virtiofs', got '%s'", call.fstype)
	}

	for _, opt := range call.options {
		if opt == "vers=3" || opt == "nconnect=16" {
			t.Errorf("VirtioFS mount should not have NFS options, got: %v", call.options)
		}
	}
}

func TestStageMount_MountError(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{mountError: errMockMount}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	err := fs.StageMountForTest(mounter, false, "", "test-disk", t.TempDir(), []string{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, node.ErrFailedMount) {
		t.Errorf("expected error to wrap ErrFailedMount, got: %v", err)
	}
}

func TestStageMount_CustomMountOptions(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	err := fs.StageMountForTest(
		mounter, false, "", "test-disk", t.TempDir(), []string{"defaults", "ro", "noatime", "nodiratime"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	call := mockMnt.mountCalls[0]
	expectedOpts := map[string]bool{"defaults": false, "ro": false, "noatime": false, "nodiratime": false}
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

func TestBindMount(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	staging := t.TempDir()
	err := fs.BindMountForTest(mounter, staging, t.TempDir(), false, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(mockMnt.mountCalls) != 1 {
		t.Fatalf("expected 1 bind mount call, got %d", len(mockMnt.mountCalls))
	}

	call := mockMnt.mountCalls[0]
	if call.source != staging {
		t.Errorf("expected bind source to be the staging path %q, got %q", staging, call.source)
	}
	if call.fstype != "" {
		t.Errorf("expected empty fstype for a bind, got %q", call.fstype)
	}
	if !containsOption(call.options, "bind") {
		t.Errorf("expected 'bind' option, got: %v", call.options)
	}
}

func TestBindMount_ReadonlyRemount(t *testing.T) {
	t.Parallel()
	mockMnt := &mockMounter{}
	mounter := &mount.SafeFormatAndMount{Interface: mockMnt}

	err := fs.BindMountForTest(mounter, t.TempDir(), t.TempDir(), true, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// A readonly bind needs the initial bind plus a remount to enforce ro.
	if len(mockMnt.mountCalls) != 2 {
		t.Fatalf("expected 2 mount calls (bind + ro remount), got %d", len(mockMnt.mountCalls))
	}
	if !containsOption(mockMnt.mountCalls[0].options, node.ReadOnlyMountOption) {
		t.Errorf("expected initial bind to carry ro, got: %v", mockMnt.mountCalls[0].options)
	}
	remount := mockMnt.mountCalls[1].options
	for _, want := range []string{"bind", "remount", node.ReadOnlyMountOption} {
		if !containsOption(remount, want) {
			t.Errorf("expected remount option %q, got: %v", want, remount)
		}
	}
}

func assertOptionsEqual(t *testing.T, expected, got []string) {
	t.Helper()

	if len(got) != len(expected) {
		t.Errorf("expected %d mount options, got %d: %v", len(expected), len(got), got)
	}

	present := make(map[string]bool, len(got))
	for _, opt := range got {
		present[opt] = true
	}
	for _, want := range expected {
		if !present[want] {
			t.Errorf("expected mount option '%s' not found in %v", want, got)
		}
	}
}

func containsOption(options []string, want string) bool {
	for _, opt := range options {
		if opt == want {
			return true
		}
	}

	return false
}
