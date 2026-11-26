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
	Unlock func()
}

// LockObjectGroup acquires one process-wide Object lock group in ObjectID order.
// The caller must invoke Unlock exactly once when the protected operation ends.
func (layer *Orchestration) LockObjectGroup(request LockObjectGroupRequest) (LockObjectGroupResponse, error) {
	group, err := layer.objectLocks.lockGroup(request.Context, request.ObjectIDs...)
	if err != nil {
		return LockObjectGroupResponse{}, err
	}
	return LockObjectGroupResponse{Unlock: group.Unlock}, nil
}
