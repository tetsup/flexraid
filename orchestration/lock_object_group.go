package orchestration

import (
	"context"

	"github.com/tetsup/flexraid/pkg/shared"
)

type LockObjectGroupRequest struct {
	Context context.Context
	ObjectIDs []shared.ObjectID
	Mode ObjectLockMode
}

type LockObjectGroupResponse struct {
	Context context.Context
	Unlock func()
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
		return LockObjectGroupResponse{Context: ctx, Unlock: func() {}}, nil
	}
	group, err := layer.objectLocks.lockGroup(ctx, request.ObjectIDs, request.Mode)
	if err != nil {
		return LockObjectGroupResponse{}, err
	}
	groupContext := context.WithValue(ctx, objectLockGroupContextKey{}, group)
	return LockObjectGroupResponse{Context: groupContext, Unlock: group.Unlock}, nil
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
