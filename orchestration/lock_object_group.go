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

// NewObjectLockGroup creates an empty, unlocked Object lock group.
func (layer *Orchestration) NewObjectLockGroup(ctx context.Context) (*ObjectLockGroup, error) {
	if ctx == nil {
		return nil, fmt.Errorf("object lock context is required")
	}
	if layer.objectLocks == nil {
		return nil, fmt.Errorf("object lock manager is not initialized")
	}
	return &ObjectLockGroup{ctx: ctx, group: newObjectLockGroup(layer.objectLocks)}, nil
}

// LockObjectGroup creates an Object lock group with a fixed read/write mode.
// Nested calls using its Context may only add ObjectIDs; they cannot change the mode.
func (layer *Orchestration) LockObjectGroup(request LockObjectGroupRequest) (LockObjectGroupResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := ctx.Value(objectLockGroupContextKey{}).(*objectLockGroup); ok && existing != nil {
		if existing.mode != request.Mode {
			return LockObjectGroupResponse{}, fmt.Errorf("object lock group mode cannot be changed")
		}
		for _, id := range uniqueSortedObjectIDs(request.ObjectIDs) {
			if err := existing.Add(ctx, id); err != nil {
				return LockObjectGroupResponse{}, err
			}
		}
		return LockObjectGroupResponse{Context: ctx, Group: &ObjectLockGroup{ctx: ctx, group: existing}, Unlock: func() {}}, nil
	}
	group, err := layer.objectLocks.lockGroup(ctx, request.ObjectIDs, request.Mode)
	if err != nil {
		return LockObjectGroupResponse{}, err
	}
	groupContext := context.WithValue(ctx, objectLockGroupContextKey{}, group)
	return LockObjectGroupResponse{Context: groupContext, Group: &ObjectLockGroup{ctx: ctx, group: group}, Unlock: group.Unlock}, nil
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
