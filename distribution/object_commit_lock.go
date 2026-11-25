package distribution

import (
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
)

type objectCommitLock struct {
	mu   sync.RWMutex
	refs uint32
}

// acquireObjectCommitLock pins a keyed lock while it is in use. Idle entries
// are removed so the lock registry does not grow with the lifetime Object set.
func (d *Distribution) acquireObjectCommitLock(objectID shared.ObjectID) *objectCommitLock {
	d.commitLocksMu.Lock()
	if d.objectCommitLocks == nil {
		d.objectCommitLocks = make(map[shared.ObjectID]*objectCommitLock)
	}
	lock := d.objectCommitLocks[objectID]
	if lock == nil {
		lock = &objectCommitLock{}
		d.objectCommitLocks[objectID] = lock
	}
	lock.refs++
	d.commitLocksMu.Unlock()
	return lock
}

func (d *Distribution) releaseObjectCommitLock(objectID shared.ObjectID, lock *objectCommitLock) {
	d.commitLocksMu.Lock()
	lock.refs--
	if lock.refs == 0 {
		delete(d.objectCommitLocks, objectID)
	}
	d.commitLocksMu.Unlock()
}

func (d *Distribution) lockObjectRead(objectID shared.ObjectID) func() {
	lock := d.acquireObjectCommitLock(objectID)
	lock.mu.RLock()
	return func() {
		lock.mu.RUnlock()
		d.releaseObjectCommitLock(objectID, lock)
	}
}

func (d *Distribution) lockObjectWrite(objectID shared.ObjectID) func() {
	lock := d.acquireObjectCommitLock(objectID)
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		d.releaseObjectCommitLock(objectID, lock)
	}
}
