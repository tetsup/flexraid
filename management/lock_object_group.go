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

// LockObjectGroup begins or extends the fixed-mode Object lock group for one logical operation.
// Nested calls using the returned Context reuse the same group.
func (layer *Management) LockObjectGroup(request LockObjectGroupRequest) (LockObjectGroupResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := layer.orchestration.LockObjectGroup(orchestration.LockObjectGroupRequest{
		Context:   ctx,
		ObjectIDs: request.ObjectIDs,
		Mode:      request.Mode,
	})
	if err != nil {
		return LockObjectGroupResponse{}, err
	}
	return LockObjectGroupResponse{Context: result.Context, Group: result.Group, Unlock: result.Unlock}, nil
}

func fileHandleObjectIDs(handle *orchestration.FileHandle) []shared.ObjectID {
	if id, ok := handle.ObjectID(); ok {
		return []shared.ObjectID{id}
	}
	return nil
}
