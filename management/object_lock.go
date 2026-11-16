package management

import (
	"context"
	"errors"
	"sort"
	"sync"
)

type ObjectLockMode uint8

const (
	ObjectLockShared ObjectLockMode = iota
	ObjectLockExclusive
)

type ObjectLockRequest struct {
	ObjectID string
	Mode     ObjectLockMode
}

var (
	ErrInvalidLockRequest = errors.New("invalid object lock request")
	ErrLockManagerClosed  = errors.New("lock manager is closed")
)

const maintenanceGate = "\x00flexraid-maintenance-gate"

// ObjectLockManager grants a set of object locks atomically. Every ordinary
// operation also holds the shared maintenance gate; maintenance holds it
// exclusively. Requests are queued FIFO to prevent writer starvation.
type ObjectLockManager struct {
	mu      sync.Mutex
	changed chan struct{}
	active  map[string]lockCount
	queue   []*lockWaiter
	closed  bool
}

type lockCount struct {
	readers int
	writer  bool
}

type lockWaiter struct {
	requests []ObjectLockRequest
	granted  chan struct{}
}

func NewObjectLockManager() *ObjectLockManager {
	return &ObjectLockManager{
		changed: make(chan struct{}),
		active:  make(map[string]lockCount),
	}
}

// AcquireOperation atomically acquires the shared maintenance gate and all
// requested Object locks. Duplicate ObjectIDs are merged, with exclusive
// access taking precedence over shared access.
func (manager *ObjectLockManager) AcquireOperation(ctx context.Context, requests []ObjectLockRequest) (func(), error) {
	normalized, err := normalizeObjectLocks(requests)
	if err != nil {
		return nil, err
	}
	normalized = append(normalized, ObjectLockRequest{ObjectID: maintenanceGate, Mode: ObjectLockShared})
	return manager.acquire(ctx, normalized)
}

// AcquireMaintenance excludes every ordinary operation and background unit.
func (manager *ObjectLockManager) AcquireMaintenance(ctx context.Context) (func(), error) {
	return manager.acquire(ctx, []ObjectLockRequest{{ObjectID: maintenanceGate, Mode: ObjectLockExclusive}})
}

func (manager *ObjectLockManager) acquire(ctx context.Context, requests []ObjectLockRequest) (func(), error) {
	if ctx == nil {
		return nil, errors.New("lock context is required")
	}
	waiter := &lockWaiter{requests: requests, granted: make(chan struct{})}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil, ErrLockManagerClosed
	}
	manager.queue = append(manager.queue, waiter)
	manager.grantWaitersLocked()
	manager.mu.Unlock()

	select {
	case <-waiter.granted:
		return manager.releaseFunc(requests), nil
	case <-ctx.Done():
		manager.mu.Lock()
		select {
		case <-waiter.granted:
			manager.mu.Unlock()
			return manager.releaseFunc(requests), nil
		default:
		}
		for index, queued := range manager.queue {
			if queued == waiter {
				manager.queue = append(manager.queue[:index], manager.queue[index+1:]...)
				break
			}
		}
		manager.grantWaitersLocked()
		manager.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (manager *ObjectLockManager) releaseFunc(requests []ObjectLockRequest) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			for _, request := range requests {
				count := manager.active[request.ObjectID]
				if request.Mode == ObjectLockExclusive {
					count.writer = false
				} else {
					count.readers--
				}
				if count.readers == 0 && !count.writer {
					delete(manager.active, request.ObjectID)
				} else {
					manager.active[request.ObjectID] = count
				}
			}
			manager.signalLocked()
			manager.grantWaitersLocked()
			manager.mu.Unlock()
		})
	}
}

func (manager *ObjectLockManager) grantWaitersLocked() {
	for len(manager.queue) > 0 {
		waiter := manager.queue[0]
		if !manager.canGrantLocked(waiter.requests) {
			return
		}
		manager.queue = manager.queue[1:]
		for _, request := range waiter.requests {
			count := manager.active[request.ObjectID]
			if request.Mode == ObjectLockExclusive {
				count.writer = true
			} else {
				count.readers++
			}
			manager.active[request.ObjectID] = count
		}
		close(waiter.granted)
		// Do not bypass an exclusive waiter, but permit a consecutive batch
		// of compatible shared waiters.
		if len(manager.queue) > 0 && !requestsAreShared(manager.queue[0].requests) {
			return
		}
	}
}

func (manager *ObjectLockManager) canGrantLocked(requests []ObjectLockRequest) bool {
	for _, request := range requests {
		count := manager.active[request.ObjectID]
		if count.writer || (request.Mode == ObjectLockExclusive && count.readers > 0) {
			return false
		}
	}
	return true
}

func (manager *ObjectLockManager) signalLocked() {
	close(manager.changed)
	manager.changed = make(chan struct{})
}

func normalizeObjectLocks(requests []ObjectLockRequest) ([]ObjectLockRequest, error) {
	modes := make(map[string]ObjectLockMode, len(requests))
	for _, request := range requests {
		if request.ObjectID == "" || request.ObjectID == maintenanceGate ||
			(request.Mode != ObjectLockShared && request.Mode != ObjectLockExclusive) {
			return nil, ErrInvalidLockRequest
		}
		if current, ok := modes[request.ObjectID]; !ok || request.Mode > current {
			modes[request.ObjectID] = request.Mode
		}
	}
	normalized := make([]ObjectLockRequest, 0, len(modes))
	for id, mode := range modes {
		normalized = append(normalized, ObjectLockRequest{ObjectID: id, Mode: mode})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ObjectID < normalized[j].ObjectID })
	return normalized, nil
}

func requestsAreShared(requests []ObjectLockRequest) bool {
	for _, request := range requests {
		if request.Mode == ObjectLockExclusive {
			return false
		}
	}
	return true
}
