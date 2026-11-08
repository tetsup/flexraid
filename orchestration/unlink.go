package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

type UnlinkRequest struct {
	Context  context.Context
	ParentID shared.ObjectID
	Name     string
}

type UnlinkResponse struct{}

// Unlink removes a name from a directory. Object-kind-specific state changes
// are kept in separate internal functions.
func (layer *Orchestration) Unlink(request UnlinkRequest) (response UnlinkResponse, err error) {
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
	if !exists {
		return response, os.ErrNotExist
	}

	switch object.header.Kind {
	case objectKindFile:
		err = layer.unlinkFile(ctx, parent, object)
	case objectKindSymlink:
		err = layer.unlinkSymlink(ctx, parent, object)
	case objectKindHardLink:
		err = layer.unlinkHardLink(ctx, parent, object)
	case objectKindDirectory:
		err = fmt.Errorf("cannot unlink a directory: %w", os.ErrInvalid)
	default:
		err = fmt.Errorf("unsupported object kind: %w", os.ErrInvalid)
	}
	return response, err
}

func (layer *Orchestration) unlinkFile(ctx context.Context, parent, object *runtimeObject) error {
	if layer.deriveHardLinkCount(object.header.ObjectID) <= 1 {
		return layer.unlinkNamedObject(ctx, parent, object)
	}
	next := object.header
	// Mutable file attributes are authoritative in runtimeFile.header. A prior
	// Write/SetAttr may have updated them without replacing the namespace copy.
	if object.file != nil {
		object.file.mu.RLock()
		next = object.file.header
		object.file.mu.RUnlock()
	}
	next.ParentID = nil
	next.Ctime = time.Now()
	if err := layer.persistObjectHeader(ctx, next);
		return err
	}
	object.header = next
	layer.headers[next.ObjectID] = next
	if object.file != nil {
		object.file.mu.Lock()
		object.file.header = next
		object.file.mu.Unlock()
	}
	delete(parent.children, next.Name)
	return nil
}

func (layer *Orchestration) unlinkSymlink(ctx context.Context, parent, object *runtimeObject) error {
	return layer.unlinkNamedObject(ctx, parent, object)
}

// unlinkNamedObject logically deletes an ordinary single-name object. Physical
// deletion is deferred to Cleanup.
func (layer *Orchestration) unlinkNamedObject(ctx context.Context, parent, object *runtimeObject) error {
	next := object.header
	next.Deleted = true
	next.Ctime = time.Now()
	if err := layer.persistObjectHeader(ctx, next); err != nil {
		return err
	}
	object.header = next
	layer.headers[next.ObjectID] = next
	delete(parent.children, next.Name)
	layer.enqueueDeletion(next.ObjectID)
	return nil
}

// unlinkHardLink removes the named alias, then serially updates its backing
// object. The two Header writes are intentionally not crash-atomic.
func (layer *Orchestration) unlinkHardLink(ctx context.Context, parent, object *runtimeObject) error {
	if object.header.TargetID == nil {
		return fmt.Errorf("HardLink %s has no target: %w", object.header.ObjectID.String(), errInvalidHeader)
	}
	target, exists := layer.objects[*object.header.TargetID]
	if !exists || target.header.Deleted || target.header.Kind != objectKindFile || target.file == nil {
		return fmt.Errorf("HardLink target is unavailable: %w", os.ErrNotExist)
	}

	nextLink := object.header
	nextLink.Deleted = true
	nextLink.Ctime = time.Now()
	if err := layer.persistObjectHeader(ctx, nextLink); err != nil {
		return err
	}
	object.header = nextLink
	layer.headers[nextLink.ObjectID] = nextLink
	delete(parent.children, nextLink.Name)
	layer.enqueueDeletion(nextLink.ObjectID)

	// Derive remaining namespace links after the alias has been unpublished.
	remaining := layer.deriveHardLinkCount(target.header.ObjectID)
	if target.header.ParentID != nil {
		remaining-- // deriveHardLinkCount includes the backing's direct name.
	}
	if remaining > 0 {
		return nil
	}

	// If no direct name or HardLink name remains, the File is logically deleted.
	nextTarget := target.header
	nextTarget.Ctime = time.Now()
	if nextTarget.ParentID == nil {
		nextTarget.Deleted = true
	}
	if err := layer.persistObjectHeader(ctx, nextTarget); err != nil {
		return err
	}
	target.header = nextTarget
	if target.file != nil {
		target.file.mu.Lock()
		target.file.header = nextTarget
		target.file.mu.Unlock()
	}
	layer.headers[nextTarget.ObjectID] = nextTarget
	if nextTarget.Deleted {
		layer.enqueueDeletion(nextTarget.ObjectID)
	}
	return nil
}

// persistObjectHeader persists a Header for its own ObjectID, not necessarily
// the shared data-bearing runtimeFile used by HardLink names.
func (layer *Orchestration) enqueueDeletion(objectID shared.ObjectID) {
	if layer.deletionQueue == nil {
		layer.deletionQueue = make(map[shared.ObjectID]struct{})
	}
	layer.deletionQueue[objectID] = struct{}{}
}

func (layer *Orchestration) persistObjectHeader(ctx context.Context, next header) error {
	// Data-bearing objects have a shared runtimeFile. Persist their Header
	// through that cache so pending stripe writes are not overwritten by a
	// stale read from Distribution.
	if next.Kind == objectKindFile {
		if object := layer.objects[next.ObjectID]; object != nil && object.file != nil {
			file := object.file
			file.mu.Lock()
			defer file.mu.Unlock()
			return layer.persistHeaderUpdateLocked(ctx, file, next)
		}
	}

	// Namespace-only objects (HardLink and Symlink) have their own Header
	// storage but no shared data runtime.
	file := &runtimeFile{
		objectID:    next.ObjectID,
		header:      next,
		stripeCache: make(map[uint64]*stripeCacheEntry),
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	return layer.persistHeaderUpdateLocked(ctx, file, next)
}
