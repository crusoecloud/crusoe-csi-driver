package node_test

import (
	"sync"
	"testing"

	"github.com/crusoecloud/crusoe-csi-driver/internal/node"
)

func TestVolumeLocksTryAcquireAndRelease(t *testing.T) {
	t.Parallel()

	locks := node.NewVolumeLocks()
	const key = "/var/lib/kubelet/pods/abc/volumes/kubernetes.io~csi/pv/mount"

	if !locks.TryAcquire(key) {
		t.Fatal("first TryAcquire on a free key should succeed")
	}

	if locks.TryAcquire(key) {
		t.Fatal("second TryAcquire while held should fail, not block")
	}

	locks.Release(key)

	if !locks.TryAcquire(key) {
		t.Fatal("TryAcquire after Release should succeed")
	}
}

// Distinct target paths must not contend. A single RWX volume is published to a
// separate target path per pod, and the CSI spec permits those calls to run
// concurrently, so keying the lock per target must not serialise them.
func TestVolumeLocksDistinctKeysDoNotContend(t *testing.T) {
	t.Parallel()

	locks := node.NewVolumeLocks()

	if !locks.TryAcquire("/target/pod-a") {
		t.Fatal("acquire on pod-a should succeed")
	}
	if !locks.TryAcquire("/target/pod-b") {
		t.Fatal("acquire on pod-b should succeed while pod-a is held")
	}
}

// Release on a key that was never acquired must be a no-op rather than a panic,
// so a deferred Release on an error path is always safe.
func TestVolumeLocksReleaseUnheldKeyIsNoop(t *testing.T) {
	t.Parallel()

	locks := node.NewVolumeLocks()
	locks.Release("/never/acquired")
}

// Exactly one of N concurrent racers may hold a given key. This is the condition
// the EBUSY race violated: two NodePublishVolume calls for the same target path
// both ran, one mounted, the other got EBUSY from mount(2).
func TestVolumeLocksOnlyOneConcurrentHolder(t *testing.T) {
	t.Parallel()

	locks := node.NewVolumeLocks()
	const key = "/target/contended"
	const racers = 64

	var (
		start    sync.WaitGroup
		done     sync.WaitGroup
		mu       sync.Mutex
		acquired int
	)

	start.Add(1)
	done.Add(racers)

	for range racers {
		go func() {
			defer done.Done()
			start.Wait()
			if locks.TryAcquire(key) {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}()
	}

	start.Done()
	done.Wait()

	if acquired != 1 {
		t.Fatalf("expected exactly 1 of %d racers to acquire the key, got %d", racers, acquired)
	}
}

// Acquire/release cycles under concurrency must not deadlock or leak keys.
func TestVolumeLocksConcurrentCyclesLeaveNoResidue(t *testing.T) {
	t.Parallel()

	locks := node.NewVolumeLocks()
	const workers = 32

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := range workers {
		go func(id int) {
			defer wg.Done()
			key := "/target/worker"
			for range 100 {
				if locks.TryAcquire(key) {
					locks.Release(key)
				}
			}
			_ = id
		}(i)
	}
	wg.Wait()

	// Every acquire was paired with a release, so the key must be free.
	if !locks.TryAcquire("/target/worker") {
		t.Fatal("key still held after all workers released; locks leaked")
	}
}

// The zero value must be usable, because the node servers embed VolumeLocks as a
// plain field rather than calling the constructor.
func TestVolumeLocksZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	var locks node.VolumeLocks

	if !locks.TryAcquire("/target/zero") {
		t.Fatal("zero-value VolumeLocks should acquire")
	}
	if locks.TryAcquire("/target/zero") {
		t.Fatal("zero-value VolumeLocks should refuse a held key")
	}
	locks.Release("/target/zero")
	if !locks.TryAcquire("/target/zero") {
		t.Fatal("zero-value VolumeLocks should reacquire after release")
	}
}
