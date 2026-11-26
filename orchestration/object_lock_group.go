package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
)

// ObjectLockMode selects the lock mode for one Lock call.
type ObjectLockMode uint8

const (
	ObjectLockWrite ObjectLockMode = iota
	ObjectLockRead
)

type objectLockEntry struct {
	mu   sync.RWMutex
	refs int
}

type objectLockManager struct {
	mu      sync.Mutex
	entries map[shared.ObjectID]*objectLockEntry
}

type objectLockGroup struct {
	mu      sync.Mutex
	manager *objectLockManager
	targets map[shared.ObjectID]struct{}
	held    map[shared.ObjectID]*objectLockEntry
	order   []shared.ObjectID
	mode    ObjectLockMode
	locked  bool
}

func newObjectLockManager() *objectLockManager {
	return &objectLockManager{entries: make(map[shared.ObjectID]*objectLockEntry)}
}

func newObjectLockGroup(manager *objectLockManager) *objectLockGroup {
	return &objectLockGroup{
		manager: manager,
		targets: make(map[shared.ObjectID]struct{}),
		held:    make(map[shared.ObjectID]*objectLockEntry),
	}
}

func (manager *objectLockManager) entry(id shared.ObjectID) *objectLockEntry {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.entries[id]
	if entry == nil {
		entry = &objectLockEntry{}
		manager.entries[id] = entry
	}
	entry.refs++
	return entry
}

func (manager *objectLockManager) put(id shared.ObjectID, entry *objectLockEntry, mode ObjectLockMode, unlock bool) {
	if unlock {
		if mode == ObjectLockRead {
			entry.mu.RUnlock()
		} else {
			entry.mu.Unlock()
		}
	}
	manager.mu.Lock()
	entry.refs--
	if entry.refs == 0 && manager.entries[id] == entry {
		delete(manager.entries, id)
	}
	manager.mu.Unlock()
}

func (manager *objectLockManager) acquire(ctx context.Context, id shared.ObjectID, mode ObjectLockMode) (*objectLockEntry, error) {
	entry := manager.entry(id)
	if err := ctx.Err(); err != nil {
		manager.put(id, entry, mode, false)
		return nil, err
	}

	acquired := false
	if mode == ObjectLockRead {
		acquired = entry.mu.TryRLock()
	} else {
		acquired = entry.mu.TryLock()
	}
	if acquired {
		if err := ctx.Err(); err != nil {
			manager.put(id, entry, mode, true)
			return nil, err
		}
		return entry, nil
	}

	locked := make(chan struct{})
	go func() {
		if mode == ObjectLockRead {
			entry.mu.RLock()
		} else {
			entry.mu.Lock()
		}
		close(locked)
	}()

	select {
	case <-locked:
		if err := ctx.Err(); err != nil {
			manager.put(id, entry, mode, true)
			return nil, err
		}
		return entry, nil
	case <-ctx.Done():
		// RWMutex has no cancellable wait. Keep the registry reference until
		// the queued waiter eventually acquires and releases the lock.
		go func() {
			<-locked
			manager.put(id, entry, mode, true)
		}()
		return nil, ctx.Err()
	}
}

// Add inserts an ObjectID into the target set. Before Lock it only records the
// target. While locked, it releases the held suffix and reacquires the new
// target and suffix in ascending ObjectID order.
func (group *objectLockGroup) Add(ctx context.Context, id shared.ObjectID) error {
	if ctx == nil {
		return fmt.Errorf("object lock context is required")
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := group.targets[id]; exists {
		return nil
	}
	group.targets[id] = struct{}{}

	if !group.locked {
		return nil
	}

	insertAt := sort.Search(len(group.order), func(i int) bool { return !objectIDLess(group.order[i], id) })
	reacquire := append([]shared.ObjectID(nil), group.order[insertAt:]...)
	for i := len(group.order) - 1; i >= insertAt; i-- {
		heldID := group.order[i]
		group.manager.put(heldID, group.held[heldID], group.mode, true)
		delete(group.held, heldID)
	}
	group.order = group.order[:insertAt]

	toAcquire := make([]shared.ObjectID, 1, len(reacquire)+1)
	toAcquire[0] = id
	toAcquire = append(toAcquire, reacquire...)
	sortObjectIDs(toAcquire)

	if err := group.acquireLocked(ctx, toAcquire); err != nil {
		group.unlockHeldLocked()
		group.locked = false
		return err
	}
	return nil
}

// Lock acquires every target in ascending ObjectID order using the requested
// mode. The mode applies only until the next Unlock.
func (group *objectLockGroup) Lock(ctx context.Context, mode ObjectLockMode) error {
	if ctx == nil {
		return fmt.Errorf("object lock context is required")
	}
	if mode != ObjectLockRead && mode != ObjectLockWrite {
		return fmt.Errorf("invalid object lock mode: %d", mode)
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.locked {
		return fmt.Errorf("object lock group is already locked")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	group.mode = mode
	ids := make([]shared.ObjectID, 0, len(group.targets))
	for id := range group.targets {
		ids = append(ids, id)
	}
	sortObjectIDs(ids)
	if err := group.acquireLocked(ctx, ids); err != nil {
		group.unlockHeldLocked()
		return err
	}
	group.locked = true
	return nil
}

func (group *objectLockGroup) acquireLocked(ctx context.Context, ids []shared.ObjectID) error {
	for _, id := range ids {
		entry, err := group.manager.acquire(ctx, id, group.mode)
		if err != nil {
			return err
		}
		group.held[id] = entry
		group.order = append(group.order, id)
	}
	return nil
}

func (group *objectLockGroup) unlockHeldLocked() {
	for i := len(group.order) - 1; i >= 0; i-- {
		id := group.order[i]
		group.manager.put(id, group.held[id], group.mode, true)
		delete(group.held, id)
	}
	group.order = nil
}

// Unlock releases every held Object in reverse order but retains the target
// set so the group can be locked again, including with a different mode.
func (group *objectLockGroup) Unlock() {
	if group == nil {
		return
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	group.unlockHeldLocked()
	group.locked = false
}

func uniqueSortedObjectIDs(ids []shared.ObjectID) []shared.ObjectID {
	unique := make(map[shared.ObjectID]struct{}, len(ids))
	result := make([]shared.ObjectID, 0, len(ids))
	for _, id := range ids {
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		result = append(result, id)
	}
	sortObjectIDs(result)
	return result
}

func sortObjectIDs(ids []shared.ObjectID) {
	sort.Slice(ids, func(i, j int) bool { return objectIDLess(ids[i], ids[j]) })
}

func objectIDLess(a, b shared.ObjectID) bool { return bytes.Compare(a[:], b[:]) < 0 }
