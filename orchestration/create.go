package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type CreateRequest struct {
	Context  context.Context
	ParentID shared.ObjectID
	Name     string
	Mode     uint32
	UID      uint32
	GID      uint32
}

type CreateResponse struct {
	ObjectID shared.ObjectID
}

// Create creates an empty regular file and publishes it in the parent
// directory only after its initial data and Header have been persisted.
func (layer *Orchestration) Create(request CreateRequest) (response CreateResponse, err error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.createLocked(request)
}

func (layer *Orchestration) createLocked(request CreateRequest) (response CreateResponse, err error) {
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
			layer.logger.Debug("orchestration Create completed",
				"parent_id", request.ParentID.String(), "name", request.Name,
				"duration", time.Since(started), "error", err)
			return
		}
		layer.logger.Debug("orchestration Create completed",
			"parent_id", request.ParentID.String(), "name", request.Name,
			"object_id", response.ObjectID.String(), "duration", time.Since(started))
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
		return response, fmt.Errorf("generate file ObjectID: %w", err)
	}
	now := time.Now()
	parentID := request.ParentID
	objectHeader := header{
		FormatVersion: headerFormatVersion,
		ObjectID:      objectID,
		ParentID:      &parentID,
		Kind:          objectKindFile,
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
		return response, fmt.Errorf("persist file %q: %w", request.Name, err)
	}

	file := &runtimeFile{
		objectID:    objectID,
		header:      objectHeader,
		stripeCache: make(map[uint64]*stripeCacheEntry),
	}
	object := &runtimeObject{header: objectHeader, file: file}
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
	return CreateResponse{ObjectID: objectID}, nil
}
