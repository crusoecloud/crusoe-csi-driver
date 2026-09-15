package fs_test

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
)

// barrierTimeout bounds the channel waits in the barrier test so a logic error
// fails fast with a clear message instead of hanging the CI job to its timeout.
const barrierTimeout = 15 * time.Second

// blockingMounter lets a test hold a node handler inside its mount(2) call so a
// second concurrent handler deterministically observes the per-key lock held.
// The interleaving is controlled by channels (a barrier), not by timing, so the
// test is reproducible rather than a hope-to-race flake.
type blockingMounter struct {
	mount.Interface

	entered chan struct{} // Mount sends here once, after the handler took its lock
	release chan struct{} // Mount blocks until this is closed

	// isMountPoint controls IsMountPoint's answer per path. NodePublishVolume asks
	// about it twice: ensureStaged asks about the staging path (true => already
	// staged, so publish skips the stage and its Crusoe API calls), and
	// nodePublishVolumeBind asks about the target path (false => bind proceeds). A
	// nil map answers false for every path. It is written once before the handlers
	// run and only read after, so it needs no lock.
	isMountPoint map[string]bool

	mu         sync.Mutex
	mountCalls int
}

func (m *blockingMounter) Mount(_, _, _ string, _ []string) error {
	m.mu.Lock()
	m.mountCalls++
	m.mu.Unlock()

	if m.entered != nil {
		m.entered <- struct{}{}
	}
	if m.release != nil {
		<-m.release
	}

	return nil
}

func (m *blockingMounter) MountSensitive(source, target, fstype string, options, sensitiveOptions []string) error {
	return m.Mount(source, target, fstype, append(options, sensitiveOptions...))
}

func (m *blockingMounter) Unmount(_ string) error                       { return nil }
func (m *blockingMounter) List() ([]mount.MountPoint, error)            { return nil, nil }
func (m *blockingMounter) IsLikelyNotMountPoint(_ string) (bool, error) { return true, nil }
func (m *blockingMounter) IsMountPoint(p string) (bool, error)          { return m.isMountPoint[p], nil }
func (m *blockingMounter) GetMountRefs(_ string) ([]string, error)      { return nil, nil }

func (m *blockingMounter) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.mountCalls
}

func mountCapability() *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
	}
}

// TestTargetLockHeldAcrossMount proves NodePublishVolume holds the per-target
// lock across the whole mount, not just at entry: while one publish is in its
// mount(2), a second publish and an unpublish for the same target are both
// refused with codes.Aborted and neither reaches mount(2). This is the concrete
// "no double-mount / no EBUSY collision under a kubelet re-drive" guarantee.
func TestTargetLockHeldAcrossMount(t *testing.T) {
	t.Parallel()

	// This test drives the real NodePublishVolume handler, which calls
	// SafeFormatAndMount.IsMountPoint before mount(2). Off linux that method is a
	// hard "unsupported platform" stub (mount_unsupported.go), so the handler would
	// error before ever reaching the mounter we block on. The driver only runs on
	// linux and CI runs there, so gate this to linux rather than assert against a stub.
	if runtime.GOOS != "linux" {
		t.Skip("SafeFormatAndMount.IsMountPoint is an unsupported-platform stub off linux; validated on CI")
	}

	staging := t.TempDir()
	target := t.TempDir()

	// Report the staging path as already mounted so publish's stage step is a no-op
	// (ensureStaged returns before any Crusoe API call) and the mount(2) this test
	// blocks on is the per-pod bind, which is the mount the target lock must cover.
	fm := &blockingMounter{
		entered:      make(chan struct{}, 1),
		release:      make(chan struct{}),
		isMountPoint: map[string]bool{staging: true},
	}
	d := &fs.Node{Mounter: &mount.SafeFormatAndMount{Interface: fm}}

	publishReq := &csi.NodePublishVolumeRequest{
		VolumeId:          "vol",
		StagingTargetPath: staging,
		TargetPath:        target,
		VolumeCapability:  mountCapability(),
	}

	// Call A: runs the real handler and blocks inside mount(2), holding the target lock.
	aDone := make(chan error, 1)
	go func() {
		_, err := d.NodePublishVolume(context.Background(), publishReq)
		aDone <- err
	}()

	select {
	case <-fm.entered: // A has taken the target lock and is now inside mount(2).
	case <-time.After(barrierTimeout):
		t.Fatal("publish A never reached mount(2); it should signal after taking the target lock")
	}

	// Call B: same target, must be refused before mounting.
	_, errB := d.NodePublishVolume(context.Background(), publishReq)
	if status.Code(errB) != codes.Aborted {
		t.Fatalf("concurrent publish on the same target: want Aborted, got %v", errB)
	}

	// Call C: unpublish shares the target key, so it cannot tear down a publish in flight.
	_, errC := d.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "vol",
		TargetPath: target,
	})
	if status.Code(errC) != codes.Aborted {
		t.Fatalf("unpublish racing a publish on the same target: want Aborted, got %v", errC)
	}

	if got := fm.calls(); got != 1 {
		t.Fatalf("exactly one mount should be in flight while the lock is held, got %d", got)
	}

	close(fm.release) // let A finish.
	select {
	case err := <-aDone:
		if err != nil {
			t.Fatalf("the winning publish should succeed, got %v", err)
		}
	case <-time.After(barrierTimeout):
		t.Fatal("publish A did not return after the mount was released")
	}

	if got := fm.calls(); got != 1 {
		t.Fatalf("only the winner should have mounted, got %d total mounts", got)
	}
}

// TestContendedKeyReturnsAborted proves every node handler checks its lock at
// entry and returns codes.Aborted when the key is already held, without reaching
// mount(2). NodeStage/NodeUnstage serialise on the staging path; NodePublish/
// NodeUnpublish on the target path. Holding the lock externally is equivalent to
// another in-flight call and keeps the test free of the Crusoe API and real
// mounts, since every handler refuses before it touches either.
func TestContendedKeyReturnsAborted(t *testing.T) {
	t.Parallel()

	fm := &blockingMounter{} // nil channels: mount would return immediately if ever reached.
	d := &fs.Node{Mounter: &mount.SafeFormatAndMount{Interface: fm}}

	staging := t.TempDir()
	target := t.TempDir()
	ctx := context.Background()

	// Target key held: publish and unpublish for that target are refused.
	if !d.VolumeLocks.TryAcquire(target) {
		t.Fatal("precondition: could not acquire the target lock")
	}
	_, err := d.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
		VolumeId: "vol", StagingTargetPath: staging, TargetPath: target, VolumeCapability: mountCapability(),
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("publish on a held target: want Aborted, got %v", err)
	}
	_, err = d.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{VolumeId: "vol", TargetPath: target})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("unpublish on a held target: want Aborted, got %v", err)
	}
	d.VolumeLocks.Release(target)

	// Staging key held: stage and unstage for that staging path are refused, and
	// stage returns before it ever reaches the NFS-flag fetch or resolveNFSTarget.
	if !d.VolumeLocks.TryAcquire(staging) {
		t.Fatal("precondition: could not acquire the staging lock")
	}
	_, err = d.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
		VolumeId: "vol", StagingTargetPath: staging, VolumeCapability: mountCapability(),
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("stage on a held staging path: want Aborted, got %v", err)
	}
	_, err = d.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{VolumeId: "vol", StagingTargetPath: staging})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("unstage on a held staging path: want Aborted, got %v", err)
	}
	// A publish arriving while a stage holds the staging path must also back off: it
	// takes the free target lock, then fails to take the staging lock for its own
	// stage step (stageForPublish), and returns Aborted without mounting. This is the
	// case the empty-mount fix adds, since publish now stages too.
	_, err = d.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
		VolumeId: "vol", StagingTargetPath: staging, TargetPath: target, VolumeCapability: mountCapability(),
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("publish while a stage holds the staging path: want Aborted, got %v", err)
	}
	d.VolumeLocks.Release(staging)

	if got := fm.calls(); got != 0 {
		t.Fatalf("a contended key must refuse before mounting, got %d mounts", got)
	}
}
