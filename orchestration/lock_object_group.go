package orchestration

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
)

type LockObjectGroupRequest struct {
	Context   context.Context
	ObjectIDs []shared.ObjectID
	Mode      ObjectLockMode
}

type LockObjectGroupResponse struct {
	Context context.Context
	Group   *ObjectLockGroup
	Unlock  func()
}

// ObjectLockGroup is a reusable logical Object lock set. Its target set is
// retained across Unlock calls; each Lock call selects the mode for that interval.
type ObjectLockGroup struct {
	ctx   context.Context
	group *objectLockGroup
}

// Add adds an ObjectID to the group. It is safe to call before or after Lock.
func (group *ObjectLockGroup) Add(id shared.ObjectID) error {
	return group.group.Add(group.ctx, id)
}

// Lock acquires all accumulated ObjectIDs in the requested mode.
func (group *ObjectLockGroup) Lock(mode ObjectLockMode) error {
	return group.group.Lock(group.ctx, mode)
}

// Unlock releases the current lock interval and retains the target set.
func (group *ObjectLockGroup) Unlock() {
	group.group.Unlock()
}

// Context returns the context that carries this group into nested layer calls.
func (group *ObjectLockGroup) Context() context.Context {
	return group.ctx
}

// NewObjectLockGroup creates an empty, unlocked Object lock group.
func (layer *Orchestration) NewObjectLockGroup(ctx context.Context) (*ObjectLockGroup, error) {
	if ctx == nil {
		return nil, fmt.Errorf("object lock context is required")
	}
	if layer.objectLocks == nil {
		return nil, fmt.Errorf("object lock manager is not initialized")
	}
	inner := newObjectLockGroup(layer.objectLocks)
	groupContext := context.WithValue(ctx, objectLockGroupContextKey{}, inner)
	return &ObjectLockGroup{ctx: groupContext, group: inner}, nil
}

// ObjectLockGroupFromContext returns the group already associated with ctx.
func (layer *Orchestration) ObjectLockGroupFromContext(ctx context.Context) (*ObjectLockGroup, bool) {
	if ctx == nil {
		return nil, false
	}
	inner, ok := ctx.Value(objectLockGroupContextKey{}).(*objectLockGroup)
	if !ok || inner == nil {
		return nil, false
	}
	return &ObjectLockGroup{ctx: ctx, group: inner}, true
}

type objectLockGroupContextKey struct{}

func addObjectLocks(ctx context.Context, ids ...shared.ObjectID) error {
	if ctx == nil {
		return context.Canceled
	}
	group, ok := ctx.Value(objectLockGroupContextKey{}).(*objectLockGroup)
	if !ok || group == nil {
		// Internal unit tests may invoke Orchestration methods directly. Runtime
		// entry points attach a group through Management.
		return nil
	}
	for _, id := range uniqueSortedObjectIDs(ids) {
		if err := group.Add(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
