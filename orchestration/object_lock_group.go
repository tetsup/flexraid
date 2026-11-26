package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
)

type objectLockEntry struct {
	token chan struct{}
	refs  int
}

type objectLockManager struct {
	mu      sync.Mutex
	entries map[shared.ObjectID]*objectLockEntry
}

type objectLockGroup struct {
	manager *objectLockManager
	held    map[shared.ObjectID]*objectLockEntry
	order   []shared.ObjectID
	closed  bool
}

func newObjectLockManager() *objectLockManager {
	return &objectLockManager{entries: make(map[shared.ObjectID]*objectLockEntry)}
}

func (manager *objectLockManager) entry(id shared.ObjectID) *objectLockEntry {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.entries[id]
	if entry == nil {
		entry = &objectLockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		manager.entries[id] = entry
	}
	entry.refs++
	return entry
}

func (manager *objectLockManager) put(id shared.ObjectID, entry *objectLockEntry, unlock bool) {
	if unlock {
		entry.token <- struct{}{}
	}
	manager.mu.Lock()
	entry.refs--
	if entry.refs == 0 && manager.entries[id] == entry {
		delete(manager.entries, id)
	}
	manager.mu.Unlock()
}

func (manager *objectLockManager) acquire(ctx context.Context, id shared.ObjectID) (*objectLockEntry, error) {
	entry := manager.entry(id)
	select {
	case <-ctx.Done():
		manager.put(id, entry, false)
		return nil, ctx.Err()
	case <-entry.token:
		if err := ctx.Err(); err != nil {
			manager.put(id, entry, true)
			return nil, err
		}
		return entry, nil
	}
}

func (manager *objectLockManager) lockGroup(ctx context.Context, ids ...shared.ObjectID) (*objectLockGroup, error) {
	if ctx == nil {
		return nil, fmt.Errorf("object lock context is required")
	}
	group := &objectLockGroup{
		manager: manager,
		held:    make(map[shared.ObjectID]*objectLockEntry),
	}
	if err := group.Add(ctx, ids...); err != nil {
		group.Unlock()
		return nil, err
	}
	return group, nil
}

// Add extends the group without ever waiting for a lower ObjectID while
// retaining a higher one. Higher held locks are released in reverse order,
// then the merged set is acquired in ascending ObjectID order.
func (group *objectLockGroup) Add(ctx context.Context, ids ...shared.ObjectID) error {
	if group.closed {
		return fmt.Errorf("object lock group is closed")
	}
	if ctx == nil {
		return fmt.Errorf("object lock context is required")
	}

	additional := make([]shared.ObjectID, 0, len(ids))
	seen := make(map[shared.ObjectID]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := group.held[id]; exists {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		additional = append(additional, id)
	}
	if len(additional) == 0 {
		return nil
	}
	sortObjectIDs(additional)

	var retained []shared.ObjectID
	var reacquire []shared.ObjectID
	if len(group.order) > 0 && objectIDLess(additional[0], group.order[len(group.order)-1]) {
		first := additional[0]
		for _, id := range group.order {
			if objectIDLess(first, id) {
				reacquire = append(reacquire, id)
			} else {
				retained = append(retained, id)
			}
		}
		for i := len(group.order) - 1; i >= 0; i-- {
			id := group.order[i]
			if _, release := group.held[id]; release && first.Less(id) {
				group.manager.put(id, group.held[id], true)
				delete(group.held, id)
			}
		}
		group.order = retained
	}

	toAcquire := append(additional, reacquire...)
	sortObjectIDs(toAcquire)
	acquired := make([]shared.ObjectID, 0, len(toAcquire))
	for _, id := range toAcquire {
		entry, err := group.manager.acquire(ctx, id)
		if err != nil {
			for i := len(acquired) - 1; i >= 0; i-- {
				heldID := acquired[i]
				group.manager.put(heldID, group.held[heldID], true)
				delete(group.held, heldID)
			}
			return err
		}
		group.held[id] = entry
		acquired = append(acquired, id)
	}
	group.order = append(group.order, acquired...)
	sortObjectIDs(group.order)
	return nil
}

// Unlock releases every lock in reverse ObjectID order. It is idempotent.
func (group *objectLockGroup) Unlock() {
	if group == nil || group.closed {
		return
	}
	group.closed = true
	for i := len(group.order) - 1; i >= 0; i-- {
		id := group.order[i]
		if entry := group.held[id]; entry != nil {
			group.manager.put(id, entry, true)
			delete(group.held, id)
		}
	}
	group.order = nil
}

func sortObjectIDs(ids []shared.ObjectID) {
	sort.Slice(ids, func(i, j int) bool {
		return bytes.Compare(ids[i][:], ids[j][:]) < 0
	})
}
