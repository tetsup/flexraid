package orchestration

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type RmdirRequest struct {
	Context  context.Context
	ParentID shared.ObjectID
	Name     string
}

type RmdirResponse struct{}

// Rmdir removes an empty directory from its parent Namespace. NLink is
// derived from the Namespace, so removing the child entry updates it without
// persisting a separate link-count field.
func (layer *Orchestration) Rmdir(request RmdirRequest) (response RmdirResponse, err error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if err = validateNewObjectName(request.Name); err != nil {
		return response, err
	}

	parent, exists := layer.objects[request.ParentID]
	if !exists || parent.header.Deleted {
		return response, os.ErrNotExist
	}
	if parent.header.Kind != objectKindDirectory {
		return response, fmt.Errorf("parent %s: %w", request.ParentID.String(), errNotDirectory)
	}
	if _, protected := layer.protectedObjects[request.ParentID]; protected {
		return response, os.ErrNotExist
	}

	object, exists := parent.children[request.Name]
	if !exists || object.header.Deleted {
		return response, os.ErrNotExist
	}
	if object.header.Kind != objectKindDirectory {
		return response, fmt.Errorf("object %q is not a directory: %w", request.Name, errNotDirectory)
	}
	if _, protected := layer.protectedObjects[object.header.ObjectID]; protected {
		return response, os.ErrNotExist
	}
	if len(object.children) != 0 {
		return response, syscall.ENOTEMPTY
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}

	next := object.header
	next.Deleted = true
	next.Ctime = time.Now()
	if err = layer.persistObjectHeader(ctx, next); err != nil {
		return response, fmt.Errorf("persist removed directory %q: %w", request.Name, err)
	}

	object.header = next
	layer.headers[next.ObjectID] = next
	delete(parent.children, request.Name)
	layer.enqueueDeletion(next.ObjectID)
	return response, nil
}
