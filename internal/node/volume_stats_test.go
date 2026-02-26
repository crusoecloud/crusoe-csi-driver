package node_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func assertGRPCCode(t *testing.T, err error, expected codes.Code) {
	t.Helper()

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != expected {
		t.Errorf("expected %s, got %s", expected, st.Code())
	}
}

func assertVolumeUsage(t *testing.T, u *csi.VolumeUsage, label string) {
	t.Helper()

	if u.GetTotal() <= 0 {
		t.Errorf("%s: expected Total > 0", label)
	}

	if u.GetAvailable() > u.GetTotal() {
		t.Errorf("%s: Available (%d) should be <= Total (%d)", label, u.GetAvailable(), u.GetTotal())
	}

	if u.GetUsed() != u.GetTotal()-u.GetAvailable() {
		t.Errorf("%s: Used (%d) should equal Total - Available (%d)", label, u.GetUsed(), u.GetTotal()-u.GetAvailable())
	}
}

func assertHealthyCondition(t *testing.T, resp *csi.NodeGetVolumeStatsResponse) {
	t.Helper()

	if resp.GetVolumeCondition() == nil {
		t.Fatal("expected VolumeCondition to be set")
	}

	if resp.GetVolumeCondition().GetAbnormal() {
		t.Error("expected VolumeCondition.Abnormal to be false")
	}
}

func TestGetVolumeStats_MissingVolumeID(t *testing.T) {
	t.Parallel()

	req := &csi.NodeGetVolumeStatsRequest{
		VolumePath: "/some/path",
	}

	_, err := node.GetVolumeStats(req)
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestGetVolumeStats_MissingVolumePath(t *testing.T) {
	t.Parallel()

	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId: "vol-123",
	}

	_, err := node.GetVolumeStats(req)
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestGetVolumeStats_NonExistentPath(t *testing.T) {
	t.Parallel()

	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-123",
		VolumePath: "/nonexistent/path/should/not/exist",
	}

	_, err := node.GetVolumeStats(req)
	assertGRPCCode(t, err, codes.NotFound)
}

func TestGetVolumeStats_FilesystemVolume(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-123",
		VolumePath: dir,
	}

	resp, err := node.GetVolumeStats(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetUsage()) != 2 {
		t.Fatalf("expected 2 usage entries (BYTES + INODES), got %d", len(resp.GetUsage()))
	}

	var foundBytes, foundInodes bool

	for _, u := range resp.GetUsage() {
		switch u.GetUnit() {
		case csi.VolumeUsage_BYTES:
			foundBytes = true
			assertVolumeUsage(t, u, "BYTES")
		case csi.VolumeUsage_INODES:
			foundInodes = true
			assertVolumeUsage(t, u, "INODES")
		case csi.VolumeUsage_UNKNOWN:
			t.Error("unexpected UNKNOWN usage unit")
		}
	}

	if !foundBytes {
		t.Error("missing BYTES usage entry")
	}

	if !foundInodes {
		t.Error("missing INODES usage entry")
	}

	assertHealthyCondition(t, resp)
}

func TestGetVolumeStats_BlockVolume(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blockPath := filepath.Join(dir, "blockdevice")

	if err := os.WriteFile(blockPath, []byte("fake block"), 0o600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-456",
		VolumePath: blockPath,
	}

	resp, err := node.GetVolumeStats(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetUsage()) != 0 {
		t.Errorf("expected 0 usage entries for block volume, got %d", len(resp.GetUsage()))
	}

	assertHealthyCondition(t, resp)
}

func TestValidateVolumeStatsRequest_Valid(t *testing.T) {
	t.Parallel()

	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "vol-123",
		VolumePath: "/some/path",
	}

	if err := node.ValidateVolumeStatsRequest(req); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestGetFilesystemVolumeStats(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	resp, err := node.GetFilesystemVolumeStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetUsage()) != 2 {
		t.Fatalf("expected 2 usage entries, got %d", len(resp.GetUsage()))
	}

	bytesUsage := resp.GetUsage()[0]
	if bytesUsage.GetUnit() != csi.VolumeUsage_BYTES {
		t.Errorf("expected first entry to be BYTES, got %v", bytesUsage.GetUnit())
	}

	if bytesUsage.GetTotal() <= 0 {
		t.Error("expected Total bytes > 0")
	}

	inodesUsage := resp.GetUsage()[1]
	if inodesUsage.GetUnit() != csi.VolumeUsage_INODES {
		t.Errorf("expected second entry to be INODES, got %v", inodesUsage.GetUnit())
	}

	if inodesUsage.GetTotal() <= 0 {
		t.Error("expected Total inodes > 0")
	}

	assertHealthyCondition(t, resp)
}
