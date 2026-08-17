package node

import (
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
)

// VolumeOperationAlreadyExistsFmt is the gRPC message returned with codes.Aborted
// when a node operation is already in flight for the same key. The CSI spec
// (NodePublishVolume / NodeUnpublishVolume, "ABORTED") tells the plugin to return
// ABORTED rather than block, so the CO can retry; every upstream CSI driver that
// implements this guard uses the same shape.
const VolumeOperationAlreadyExistsFmt = "an operation with the given key %s already exists"

// VolumeLocks serialises node operations by an arbitrary key, non-blocking.
//
// Why non-blocking: the CO (kubelet) drives NodePublishVolume with its own
// deadline, currently 2 minutes. Blocking inside the handler would hold a gRPC
// worker for the duration and the CO would time out anyway, so the useful
// behaviour is to refuse immediately with codes.Aborted and let the CO retry.
//
// This is the same helper csi-driver-nfs carries in pkg/nfs/utils.go, which is
// the closest analogue upstream: it also leaves NodeStageVolume unimplemented
// and locks both publish and unpublish. It differs only in using
// sets.Set[string] rather than the now-deprecated sets.String, and in the usable
// zero value below. The AWS EBS driver has the same non-blocking shape under
// different names (internal.InFlight, with Insert and Delete).
//
// Why an explicit set rather than sync.Map or keymutex: keymutex only offers a
// blocking Lock, and a plain map of mutexes never reclaims entries. Tracking held
// keys in a set keeps the footprint proportional to in-flight operations rather
// than to every volume the node has ever seen.
type VolumeLocks struct {
	locks sets.Set[string]
	mux   sync.Mutex
}

// NewVolumeLocks returns an initialised VolumeLocks. The zero value is also
// usable, so embedding VolumeLocks as a plain field needs no constructor call —
// deliberate, because this repo builds its node servers with struct literals and
// a forgotten init on a *VolumeLocks would panic inside a gRPC handler and take
// the pod with it.
func NewVolumeLocks() *VolumeLocks {
	return &VolumeLocks{
		locks: sets.New[string](),
	}
}

// TryAcquire takes the lock for key and reports whether it succeeded. It never
// blocks: a false return means another operation holds the key, and the caller
// should return codes.Aborted.
func (vl *VolumeLocks) TryAcquire(key string) bool {
	vl.mux.Lock()
	defer vl.mux.Unlock()

	if vl.locks == nil {
		vl.locks = sets.New[string]()
	}

	if vl.locks.Has(key) {
		return false
	}
	vl.locks.Insert(key)

	return true
}

// Release drops the lock for key. Releasing a key that is not held is a no-op,
// so a deferred Release is safe on every path including early returns.
func (vl *VolumeLocks) Release(key string) {
	vl.mux.Lock()
	defer vl.mux.Unlock()

	vl.locks.Delete(key)
}
