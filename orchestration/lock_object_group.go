package orchestration

import (
	"context"

	"github.com/tetsup/flexraid/pkg/shared"
)

type LockObjectGroupRequest struct {
	Context   context.Context
	ObjectIDs []shared.ObjectID
}

type LockObjectGroupResponse struct {
	Context context.Context
	Unlock  func()
}

// LockObjectGroup acquires one process-wide Object lock group in ObjectID order.
// The caller must invoke Unlock exactly once when the protected operation ends.
func (layer *Orchestration) LockObjectGroup(request LockObjectGroupRequest) (LockObjectGroupResponse, error) {
	group, err := layer.objectLocks.lockGroup(request.Context, request.ObjectIDs...)
	if err != nil {
		return LockObjectGroupResponse{}, err
	}
	groupContext := context.WithValue(request.Context, objectLockGroupContextKey{}, group)
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
		// entry points always attach a group through Management.
		return nil
	}
	return group.Add(ctx, ids...)
}
