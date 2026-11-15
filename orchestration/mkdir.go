package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type MkdirRequest struct {
	Context  context.Context
	ParentID shared.ObjectID
	Name     string
	Mode     uint32
	UID      uint32
	GID      uint32
}

type MkdirResponse struct {
	ObjectID shared.ObjectID
}

// Mkdir creates and persists a Directory before publishing it in its parent.
func (layer *Orchestration) MkdirLocked(request MkdirRequest) (response MkdirResponse, err error) {
	return layer.mkdirLocked(request)
}

func (layer *Orchestration) mkdirLocked(request MkdirRequest) (response MkdirResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if layer.logger == nil {
			return
		}
		if err != nil {
			layer.logger.Debug("orchestration Mkdir completed", "parent_id", request.ParentID.String(), "name", request.Name, "duration", time.Since(started), "error", err)
			return
		}
		layer.logger.Debug("orchestration Mkdir completed", "parent_id", request.ParentID.String(), "name", request.Name, "object_id", response.ObjectID.String(), "duration", time.Since(started))
	}()

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
	if _, exists := parent.children[request.Name]; exists {
		return response, os.ErrExist
	}

	objectID, err := shared.NewObjectID()
	if err != nil {
		return response, fmt.Errorf("generate directory ObjectID: %w", err)
	}
	now := time.Now()
	parentID := request.ParentID
	objectHeader := header{
		FormatVersion: headerFormatVersion,
		ObjectID:      objectID,
		ParentID:      &parentID,
		Kind:          objectKindDirectory,
		Name:          request.Name,
		Mode:          request.Mode,
		UID:           request.UID,
		GID:           request.GID,
		Atime:         now,
		Mtime:         now,
		Ctime:         now,
	}
	objectHeader, err = layer.persistNewObject(ctx, objectHeader, nil)
	if err != nil {
		return response, fmt.Errorf("persist directory %q: %w", request.Name, err)
	}

	object := &runtimeObject{header: objectHeader, children: make(map[string]*runtimeObject)}
	if layer.objects == nil {
		layer.objects = make(map[shared.ObjectID]*runtimeObject)
	}
	if layer.headers == nil {
		layer.headers = make(map[shared.ObjectID]header)
	}
	if err = layer.touchDirectoryLocked(ctx, parent, now); err != nil {
		return response, fmt.Errorf("update parent directory timestamps: %w", err)
	}
	parent.children[request.Name] = object
	layer.objects[objectID] = object
	layer.headers[objectID] = objectHeader
	return MkdirResponse{ObjectID: objectID}, nil
}
