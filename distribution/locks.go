package distribution

import "sync"

// lockStripe acquires the map mutex before the per-stripe mutex. The map mutex
// is released once the stripe mutex is held, so unrelated stripes can proceed.
func (d *Distribution) lockStripe(key stripeIDKey) func() {
	d.mapMu.Lock()
	lock := d.stripeLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		d.stripeLocks[key] = lock
	}
	lock.Lock()
	d.mapMu.Unlock()
	return lock.Unlock
}
