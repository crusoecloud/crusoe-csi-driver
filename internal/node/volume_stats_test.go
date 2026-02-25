package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetFilesystemStats(t *testing.T) {
	dir := t.TempDir()

	usage, err := GetFilesystemStats(dir)
	if err != nil {
		t.Fatalf("GetFilesystemStats(%s) returned error: %v", dir, err)
	}

	if len(usage) != 2 {
		t.Fatalf("expected 2 usage entries (bytes + inodes), got %d", len(usage))
	}

	bytesUsage := usage[0]
	inodesUsage := usage[1]

	if bytesUsage.Unit != csi.VolumeUsage_BYTES {
		t.Errorf("expected first entry unit BYTES, got %v", bytesUsage.Unit)
	}

	if bytesUsage.Total <= 0 {
		t.Errorf("expected total bytes > 0, got %d", bytesUsage.Total)
	}

	if bytesUsage.Available < 0 {
		t.Errorf("expected available bytes >= 0, got %d", bytesUsage.Available)
	}

	if inodesUsage.Unit != csi.VolumeUsage_INODES {
		t.Errorf("expected second entry unit INODES, got %v", inodesUsage.Unit)
	}

	if inodesUsage.Total <= 0 {
		t.Errorf("expected total inodes > 0, got %d", inodesUsage.Total)
	}

	if inodesUsage.Available < 0 {
		t.Errorf("expected available inodes >= 0, got %d", inodesUsage.Available)
	}
}

func TestGetFilesystemStatsNonExistentPath(t *testing.T) {
	_, err := GetFilesystemStats("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

func TestIsBlockDeviceDirectory(t *testing.T) {
	dir := t.TempDir()

	isBlock, err := IsBlockDevice(dir)
	if err != nil {
		t.Fatalf("IsBlockDevice(%s) returned error: %v", dir, err)
	}

	if isBlock {
		t.Errorf("expected directory to not be a block device")
	}
}

func TestIsBlockDeviceFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "blockdev")

	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	isBlock, err := IsBlockDevice(filePath)
	if err != nil {
		t.Fatalf("IsBlockDevice(%s) returned error: %v", filePath, err)
	}

	if !isBlock {
		t.Errorf("expected regular file to be identified as block device")
	}
}

func TestIsBlockDeviceNonExistent(t *testing.T) {
	_, err := IsBlockDevice("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}
