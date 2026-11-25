package distribution

import (
	"runtime"
	"sync"
)

// lockStripe acquires mapMu before the per-stripe mutex and returns with both
// held. A contended stripe never makes a waiter retain mapMu.
func (d *Distribution) lockStripe(key stripeIDKey) (unlockStripe func(), unlockMap func()) {
	d.mapMu.Lock()
	lock := d.stripeLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		d.stripeLocks[key] = lock
	}
	for !lock.TryLock() {
		d.mapMu.Unlock()
		runtime.Gosched()
		d.mapMu.Lock()
	}
	return lock.Unlock, d.mapMu.Unlock
}
