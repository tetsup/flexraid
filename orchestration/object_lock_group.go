package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/tetsup/flexraid/pkg/shared"
)

// ObjectLockMode is fixed for the lifetime of an Object lock group.
type ObjectLockMode uint8

const (
	ObjectLockWrite ObjectLockMode = iota
	ObjectLockRead
)

type objectLockEntry struct {
	mu sync.RWMutex
	refs int
}

type objectLockManager struct {
	mu sync.Mutex
	entries map[shared.ObjectID]*objectLockEntry
}

type objectLockGroup struct {
	mu sync.Mutex
	manager *objectLockManager
	mode ObjectLockMode
	held map[shared.ObjectID]*objectLockEntry
	order []shared.ObjectID
	closed bool
}

func newObjectLockManager() *objectLockManager {
	return &objectLockManager{entries: make(map[shared.ObjectID]*objectLockEntry)}
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
		if mode == ObjectLockRead { entry.mu.RUnlock() } else { entry.mu.Unlock() }
	}
	manager.mu.Lock()
	entry.refs--
	if entry.refs == 0 && manager.entries[id] == entry { delete(manager.entries, id) }
	manager.mu.Unlock()
}

func (manager *objectLockManager) acquire(ctx context.Context, id shared.ObjectID, mode ObjectLockMode) (*objectLockEntry, error) {
	entry := manager.entry(id)
	if mode == ObjectLockRead { entry.mu.RLock() } else { entry.mu.Lock() }
	if err := ctx.Err(); err != nil {
		manager.put(id, entry, mode, true)
		return nil, err
	}
	return entry, nil
}

func (manager *objectLockManager) lockGroup(ctx context.Context, ids []shared.ObjectID, mode ObjectLockMode) (*objectLockGroup, error) {
	if ctx == nil { return nil, fmt.Errorf("object lock context is required") }
	if mode != ObjectLockRead && mode != ObjectLockWrite { return nil, fmt.Errorf("invalid object lock mode: %d", mode) }
	group := &objectLockGroup{manager: manager, mode: mode, held: make(map[shared.ObjectID]*objectLockEntry)}
	for _, id := range uniqueSortedObjectIDs(ids) {
		if err := group.Add(ctx, id); err != nil { group.Unlock(); return nil, err }
	}
	return group, nil
}

// Add inserts one Object. The held suffix is released in reverse order and
// reacquired together with the new Object in ascending ObjectID order.
func (group *objectLockGroup) Add(ctx context.Context, id shared.ObjectID) error {
	if ctx == nil { return fmt.Errorf("object lock context is required") }
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.closed { return fmt.Errorf("object lock group is closed") }
	if err := ctx.Err(); err != nil { return err }
	if _, exists := group.held[id]; exists { return nil }

	insertAt := sort.Search(len(group.order), func(i int) bool { return !objectIDLess(group.order[i], id) })
	reacquire := append([]shared.ObjectID(nil), group.order[insertAt:]...)
	for i := len(group.order)-1; i >= insertAt; i-- {
		heldID := group.order[i]
		group.manager.put(heldID, group.held[heldID], group.mode, true)
		delete(group.held, heldID)
	}
	group.order = group.order[:insertAt]

	toAcquire := make([]shared.ObjectID, 1, len(reacquire)+1)
	toAcquire[0] = id
	toAcquire = append(toAcquire, reacquire...)
	sortObjectIDs(toAcquire)

	acquired := make([]shared.ObjectID, 0, len(toAcquire))
	for _, nextID := range toAcquire {
		entry, err := group.manager.acquire(ctx, nextID, group.mode)
		if err != nil {
			for i := len(acquired)-1; i >= 0; i-- {
				heldID := acquired[i]
				group.manager.put(heldID, group.held[heldID], group.mode, true)
				delete(group.held, heldID)
			}
			return err
		}
		group.held[nextID] = entry
		acquired = append(acquired, nextID)
	}
	group.order = append(group.order, acquired...)
	return nil
}

// Unlock releases all held Objects in reverse ObjectID order. It is idempotent.
func (group *objectLockGroup) Unlock() {
	if group == nil { return }
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.closed { return }
	group.closed = true
	for i := len(group.order)-1; i >= 0; i-- {
		id := group.order[i]
		group.manager.put(id, group.held[id], group.mode, true)
		delete(group.held, id)
	}
	group.order = nil
}

func uniqueSortedObjectIDs(ids []shared.ObjectID) []shared.ObjectID {
	unique := make(map[shared.ObjectID]struct{}, len(ids))
	result := make([]shared.ObjectID, 0, len(ids))
	for _, id := range ids {
		if _, exists := unique[id]; exists { continue }
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
