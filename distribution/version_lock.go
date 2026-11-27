package distribution

import (
	"sort"
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

func (d *Distribution) lockSelectedStripeRead(key stripeIDKey) (*stripeInfo, func(), error) {
	for {
		d.mapMu.RLock()
		stripes := d.objects[key.objectID]
		if stripes == nil || stripes[key.stripeID] == nil {
			d.mapMu.RUnlock()
			return nil, nil, ErrStripeNotFound
		}
		version := stripes[key.stripeID].version
		d.mapMu.RUnlock()

		unlock := d.lockVersionRead(key.objectID, key.stripeID, version)
		d.mapMu.RLock()
		stripes = d.objects[key.objectID]
		if stripes == nil || stripes[key.stripeID] == nil {
			d.mapMu.RUnlock()
			unlock()
			return nil, nil, ErrStripeNotFound
		}
		source := stripes[key.stripeID]
		if source.version != version {
			d.mapMu.RUnlock()
			unlock()
			continue
		}
		info := &stripeInfo{
			version: source.version, method: source.method, chunkSize: source.chunkSize,
			chunks: cloneChunks(source.chunks), corrupted: source.corrupted,
		}
		d.mapMu.RUnlock()
		return info, unlock, nil
	}
}

func (d *Distribution) lockAllVersionsForObject(objectID shared.ObjectID) []func() {
	for {
		keys := d.objectVersionKeys(objectID)
		unlocks := make([]func(), 0, len(keys))
		for _, key := range keys {
			unlocks = append(unlocks, d.lockVersionWrite(key.objectID, key.stripeID, key.version))
		}
		if d.objectVersionKeysCovered(objectID, keys) {
			return unlocks
		}
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

func (d *Distribution) objectVersionKeys(objectID shared.ObjectID) []versionLockKey {
	d.mapMu.RLock()
	defer d.mapMu.RUnlock()
	set := make(map[versionLockKey]struct{})
	for key, versions := range d.versions {
		if key.objectID != objectID {
			continue
		}
		for version := range versions {
			set[versionLockKey{objectID: objectID, stripeID: key.stripeID, version: version}] = struct{}{}
		}
	}
	for stripeID, stripe := range d.objects[objectID] {
		for _, location := range stripe.chunks {
			set[versionLockKey{objectID: objectID, stripeID: stripeID, version: location.key.StripeVersion}] = struct{}{}
		}
	}
	for _, location := range d.orphans[objectID] {
		set[versionLockKey{objectID: objectID, stripeID: location.key.StripeID, version: location.key.StripeVersion}] = struct{}{}
	}
	keys := make([]versionLockKey, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].stripeID != keys[j].stripeID {
			return keys[i].stripeID < keys[j].stripeID
		}
		return keys[i].version < keys[j].version
	})
	return keys
}

func (d *Distribution) objectVersionKeysCovered(objectID shared.ObjectID, locked []versionLockKey) bool {
	lockedSet := make(map[versionLockKey]struct{}, len(locked))
	for _, key := range locked {
		lockedSet[key] = struct{}{}
	}
	for _, key := range d.objectVersionKeys(objectID) {
		if _, ok := lockedSet[key]; !ok {
			return false
		}
	}
	return true
}
