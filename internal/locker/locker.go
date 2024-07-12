package locker

import (
	"sync"
)

type Locker struct {
	locks map[string]*sync.RWMutex // Maps ID to its mutex for locking.
	mx    *sync.Mutex
}

func NewLocker() *Locker {
	return &Locker{
		locks: make(map[string]*sync.RWMutex),
		mx:    &sync.Mutex{},
	}
}

func (l *Locker) TryAcquireReadLock(id string) bool {
	l.mx.Lock()
	rwLock, ok := l.locks[id]
	if !ok {
		rwLock = &sync.RWMutex{}
		l.locks[id] = rwLock
	}
	l.mx.Unlock()

	return rwLock.TryRLock()
}

func (l *Locker) ReleaseReadLock(id string) {
	rwLock, ok := l.locks[id]
	if ok {
		rwLock.RUnlock()
	}
}

func (l *Locker) TryAcquireWriteLock(id string) bool {
	l.mx.Lock()
	rwLock, ok := l.locks[id]
	if !ok {
		rwLock = &sync.RWMutex{}
		l.locks[id] = rwLock
	}
	l.mx.Unlock()

	return rwLock.TryLock()
}

// ReleaseWriteLock releases a previously acquired write lock for the given ID.
func (l *Locker) ReleaseWriteLock(id string) {
	rwLock, ok := l.locks[id]
	if ok {
		rwLock.Unlock()
	}
}
