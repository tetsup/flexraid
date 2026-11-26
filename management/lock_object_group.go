package management

import (
	"context"
	"fmt"

	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
)

type LockObjectGroupRequest struct {
	Context   context.Context
	ObjectIDs []shared.ObjectID
	Mode      orchestration.ObjectLockMode
}

type LockObjectGroupResponse struct {
	Context context.Context
	Group   *orchestration.ObjectLockGroup
	Unlock  func()
}

// NewObjectLockGroup creates an empty Object lock group owned by Orchestration.
func (layer *Management) NewObjectLockGroup(ctx context.Context) (*orchestration.ObjectLockGroup, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if layer.orchestration == nil {
		return nil, fmt.Errorf("Orchestration layer is not initialized")
	}
	return layer.orchestration.NewObjectLockGroup(ctx)
}

// LockObjectGroup adds the requested Objects to the current operation group,
// creating and locking a new group only at the outermost call.
func (layer *Management) LockObjectGroup(request LockObjectGroupRequest) (LockObjectGroupResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if layer.orchestration == nil {
		return LockObjectGroupResponse{}, fmt.Errorf("Orchestration layer is not initialized")
	}
	group, nested := layer.orchestration.ObjectLockGroupFromContext(ctx)
	if !nested {
		var err error
		group, err = layer.NewObjectLockGroup(ctx)
		if err != nil {
			return LockObjectGroupResponse{}, err
		}
	}
	for _, id := range request.ObjectIDs {
		if err := group.Add(id); err != nil {
			if !nested {
				group.Unlock()
			}
			return LockObjectGroupResponse{}, err
		}
	}
	if nested {
		return LockObjectGroupResponse{Context: group.Context(), Group: group, Unlock: func() {}}, nil
	}
	if err := group.Lock(request.Mode); err != nil {
		group.Unlock()
		return LockObjectGroupResponse{}, err
	}
	return LockObjectGroupResponse{Context: group.Context(), Group: group, Unlock: group.Unlock}, nil
}

func fileHandleObjectIDs(handle *orchestration.FileHandle) []shared.ObjectID {
	if id, ok := handle.ObjectID(); ok {
		return []shared.ObjectID{id}
	}
	return nil
}
