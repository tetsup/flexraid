package orchestration

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

type LinkRequest struct {
	Context  context.Context
	SourceID shared.ObjectID
	ParentID shared.ObjectID
	Name     string
}

type LinkResponse struct {
	ObjectID shared.ObjectID
}

// Link creates a namespace entry that directly references the source File data object.
func (layer *Orchestration) LinkLocked(request LinkRequest) (response LinkResponse, err error) {
	started := time.Now()
	defer func() {
		layer.recordOperation("Link", started, err)
		if err != nil {
			if layer.logger != nil {
				layer.logger.Error("namespace link creation failed", "source_id", request.SourceID.String(), "parent_id", request.ParentID.String(), "name", request.Name, "error", err)
			}
		}
	}()
	response, err = layer.linkLocked(request)
	if err != nil {
		return response, err
	}
	parent := layer.objects[request.ParentID]
	if err = layer.touchDirectoryLocked(request.Context, parent, time.Now()); err != nil {
		return LinkResponse{}, fmt.Errorf("update parent directory timestamps: %w", err)
	}
	return response, nil
}

func (layer *Orchestration) linkLocked(request LinkRequest) (response LinkResponse, err error) {
	return layer.linkLockedWithID(request, nil)
}

func (layer *Orchestration) linkLockedWithID(request LinkRequest, requestedID *shared.ObjectID) (response LinkResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if layer.logger != nil {
			layer.logger.Debug("orchestration Link completed",
				"source_id", request.SourceID.String(),
				"parent_id", request.ParentID.String(),
				"name", request.Name,
				"object_id", response.ObjectID.String(),
				"duration", time.Since(started),
				"error", err)
		}
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

	source, exists := layer.objects[request.SourceID]
	if !exists || source.header.Deleted {
		return response, os.ErrNotExist
	}
	if _, protected := layer.protectedObjects[request.SourceID]; protected {
		return response, os.ErrNotExist
	}
	if source.header.Kind != objectKindFile && source.header.Kind != objectKindHardLink {
		if source.header.Kind == objectKindDirectory {
			return response, syscall.EPERM
		}
		return response, syscall.EPERM
	}
	if source.file == nil {
		return response, fmt.Errorf("source file backing is unavailable: %w", os.ErrNotExist)
	}

	file := source.file
	if err = ctx.Err(); err != nil {
		return response, err
	}

	backingID := file.objectID
	backingHeader := file.header
	var linkID shared.ObjectID
	if requestedID != nil {
		linkID = *requestedID
	} else {
		linkID, err = shared.NewObjectID()
		if err != nil {
			return response, fmt.Errorf("generate HardLink ObjectID: %w", err)
		}
	}
	now := time.Now()
	parentID := request.ParentID
	linkHeader := header{
		FormatVersion: headerFormatVersion,
		ObjectID:      linkID,
		ParentID:      &parentID,
		TargetID:      &backingID,
		Kind:          objectKindHardLink,
		Name:          request.Name,
		Mode:          backingHeader.Mode,
		UID:           backingHeader.UID,
		GID:           backingHeader.GID,
		Size:          0,
		Atime:         backingHeader.Atime,
		Mtime:         backingHeader.Mtime,
		Ctime:         now,
		Layout:        backingHeader.Layout,
	}
	if err = addObjectLocks(ctx, request.SourceID, backingID, linkID); err != nil {
		return response, err
	}
	if layer.objects[request.ParentID] != parent || parent.header.Deleted || parent.children[request.Name] != nil || layer.objects[request.SourceID] != source || source.file != file {
		return response, syscall.EAGAIN
	}
	linkHeader, err = layer.persistNewObject(ctx, linkHeader, nil)
	if err != nil {
		return response, fmt.Errorf("persist HardLink %q: %w", request.Name, err)
	}

	newLink := &runtimeObject{header: linkHeader, file: file}
	parent.children[request.Name] = newLink
	layer.objects[linkID] = newLink
	layer.headers[linkID] = linkHeader
	return LinkResponse{ObjectID: linkID}, nil
}

// persistHeaderUpdateLocked writes the authoritative Header to Stripe 0.
// The caller holds file.mu; the in-memory Header changes only after persistence.
func (layer *Orchestration) persistHeaderUpdateLocked(ctx context.Context, file *runtimeFile, next header) error {
	headerBytes, err := next.MarshalBinary()
	if err != nil {
		return err
	}
	if uint64(len(headerBytes)) > cfg.HeaderReservedSize {
		return fmt.Errorf("serialized Header exceeds reserved area")
	}
	geometry, err := geometryFor(file.header, storage.StripeID(0))
	if err != nil {
		return err
	}
	entry, err := layer.loadStripeForWriteLocked(ctx, file, geometry)
	if err != nil {
		return err
	}
	if len(entry.data) < int(cfg.HeaderReservedSize) {
		entry.data = append(entry.data, make([]byte, int(cfg.HeaderReservedSize)-len(entry.data))...)
	}
	copy(entry.data[:len(headerBytes)], headerBytes)
	entry.state = stripeCacheDirty

	staged := &runtimeFile{
		objectID:         file.objectID,
		header:           next,
		stripeCache:      cloneStripeCache(file.stripeCache),
		zeroStripeRanges: cloneStripeRanges(file.zeroStripeRanges),
	}
	staged.stripeCache[0] = entry
	if _, err := layer.persistDirtyStripesLocked(ctx, staged); err != nil {
		return err
	}
	file.header = next
	file.stripeCache = staged.stripeCache
	file.zeroStripeRanges = staged.zeroStripeRanges
	return nil
}
