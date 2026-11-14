package orchestration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type SymlinkRequest struct {
	Context  context.Context
	ParentID shared.ObjectID
	Name     string
	Target   string
	UID      uint32
	GID      uint32
}

type SymlinkResponse struct {
	ObjectID shared.ObjectID
}

// Symlink stores the target path as ordinary logical data and publishes the
// namespace entry only after all target-data stripes have been persisted.
func (layer *Orchestration) Symlink(request SymlinkRequest) (response SymlinkResponse, err error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.symlinkLocked(request)
}

func (layer *Orchestration) symlinkLocked(request SymlinkRequest) (response SymlinkResponse, err error) {
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
			layer.logger.Debug("orchestration Symlink completed", "parent_id", request.ParentID.String(), "name", request.Name, "target_bytes", len(request.Target), "duration", time.Since(started), "error", err)
			return
		}
		layer.logger.Debug("orchestration Symlink completed", "parent_id", request.ParentID.String(), "name", request.Name, "target_bytes", len(request.Target), "object_id", response.ObjectID.String(), "duration", time.Since(started))
	}()

	if err = ctx.Err(); err != nil {
		return response, err
	}
	if err = validateNewObjectName(request.Name); err != nil {
		return response, err
	}
	if request.Target == "" {
		return response, syscall.ENOENT
	}
	if strings.IndexByte(request.Target, 0) >= 0 {
		return response, syscall.EINVAL
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
		return response, fmt.Errorf("generate symlink ObjectID: %w", err)
	}
	now := time.Now()
	parentID := request.ParentID
	objectHeader := header{
		FormatVersion: headerFormatVersion,
		ObjectID:      objectID,
		ParentID:      &parentID,
		Kind:          objectKindSymlink,
		Name:          request.Name,
		Mode:          0777,
		UID:           request.UID,
		GID:           request.GID,
		Size:          uint64(len(request.Target)),
		Atime:         now,
		Mtime:         now,
		Ctime:         now,
	}
	objectHeader, err = layer.persistNewObject(ctx, objectHeader, []byte(request.Target))
	if err != nil {
		return response, fmt.Errorf("persist symlink %q: %w", request.Name, err)
	}

	object := &runtimeObject{header: objectHeader, linkTarget: request.Target, linkTargetLoaded: true}
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
	return SymlinkResponse{ObjectID: objectID}, nil
}
