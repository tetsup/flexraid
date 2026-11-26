package distribution

import (
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

// versionLockKey identifies one immutable physical Stripe Version.
type versionLockKey struct {
	objectID shared.ObjectID
	stripeID storage.StripeID
	version  storage.StripeVersion
}

// versionLockEntry remains registered while it has holders or waiters.
type versionLockEntry struct {
	mu   sync.RWMutex
	refs uint64
}

func (d *Distribution) lockVersionRead(objectID shared.ObjectID, stripeID storage.StripeID, version storage.StripeVersion) func() {
	return d.lockVersion(versionLockKey{objectID: objectID, stripeID: stripeID, version: version}, false)
}

func (d *Distribution) lockVersionWrite(objectID shared.ObjectID, stripeID storage.StripeID, version storage.StripeVersion) func() {
	return d.lockVersion(versionLockKey{objectID: objectID, stripeID: stripeID, version: version}, true)
}

// lockVersion pins the registry entry before waiting for its RWMutex. This
// prevents a waiter and a concurrent caller from obtaining different mutexes
// for the same key while the first holder is releasing it.
func (d *Distribution) lockVersion(key versionLockKey, write bool) func() {
	entry := d.acquireVersionLock(key)
	if write {
		entry.mu.Lock()
	} else {
		entry.mu.RLock()
	}

	return func() {
		if write {
			entry.mu.Unlock()
		} else {
			entry.mu.RUnlock()
		}
		d.releaseVersionLock(key, entry)
	}
}

func (d *Distribution) acquireVersionLock(key versionLockKey) *versionLockEntry {
	d.versionLocksMu.Lock()
	defer d.versionLocksMu.Unlock()

	if d.versionLocks == nil {
		d.versionLocks = make(map[versionLockKey]*versionLockEntry)
	}
	entry := d.versionLocks[key]
	if entry == nil {
		entry = &versionLockEntry{}
		d.versionLocks[key] = entry
	}
	entry.refs++
	return entry
}

func (d *Distribution) releaseVersionLock(key versionLockKey, entry *versionLockEntry) {
	d.versionLocksMu.Lock()
	defer d.versionLocksMu.Unlock()

	entry.refs--
	if entry.refs == 0 && d.versionLocks[key] == entry {
		delete(d.versionLocks, key)
	}
}
