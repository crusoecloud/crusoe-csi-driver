package node

import (
	"sync"
)

// VolumeOperationAlreadyExistsFmt is the gRPC message returned with codes.Aborted
// when a node operation is already in flight for the same key. The CSI spec
// (NodePublishVolume / NodeUnpublishVolume, "ABORTED") tells the plugin to return
// ABORTED rather than block, so kubelet can retry.
const VolumeOperationAlreadyExistsFmt = "an operation with the given key %s already exists"

// VolumeLocks serialises node operations by an arbitrary key, non-blocking.
//
// Why non-blocking: kubelet drives NodePublishVolume with its own deadline,
// currently 2 minutes. Blocking inside the handler would hold a gRPC worker for
// the duration and kubelet would time out anyway, so the useful behaviour is to
// refuse immediately with codes.Aborted and let kubelet retry.
//
// sync.Map.LoadOrStore is already the test-and-set this needs: it stores the key
// and reports whether it was there first. The peer CSI drivers hand-roll the
// equivalent over a mutex plus a set (csi-driver-nfs and azuredisk over the
// deprecated sets.String, AWS EBS over map[string]bool), which is more code for
// the same semantics. keymutex is the wrong shape, offering only a blocking Lock.
//
// The zero value is ready to use, which is what lets the node servers embed this
// as a plain field and build themselves with struct literals.
type VolumeLocks struct {
	inFlight sync.Map
}

// TryAcquire takes the lock for key and reports whether it succeeded. It never
// blocks: a false return means another operation holds the key, and the caller
// should return codes.Aborted.
func (vl *VolumeLocks) TryAcquire(key string) bool {
	_, alreadyHeld := vl.inFlight.LoadOrStore(key, struct{}{})

	return !alreadyHeld
}

// Release drops the lock for key. Releasing a key that is not held is a no-op,
// so a deferred Release is safe on every path including early returns.
func (vl *VolumeLocks) Release(key string) {
	vl.inFlight.Delete(key)
}
