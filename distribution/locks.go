package distribution

import (
	"runtime"
	"sync"
)

// lockStripe acquires the map mutex before the per-stripe mutex. TryLock keeps
// a waiter from holding mapMu while another operation owns the stripe lock.
// The returned unlock function releases only the stripe lock.
func (d *Distribution) lockStripe(key stripeIDKey) func() {
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
	d.mapMu.Unlock()
	return lock.Unlock
}
