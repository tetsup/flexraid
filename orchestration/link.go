package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/distribution"
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

// Link adds a new namespace name for a regular file's data object.
// Link creates a namespace entry that directly references the source File data object.
func (layer *Orchestration) Link(request LinkRequest) (response LinkResponse, err error) {
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
		return response, fmt.Errorf("source %s is not a regular file: %w", request.SourceID.String(), os.ErrInvalid)
	}
	if source.file == nil {
		return response, fmt.Errorf("source file backing is unavailable: %w", os.ErrNotExist)
	}

	file := source.file
	file.mu.Lock()
	defer file.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return response, err
	}

	backingID := file.objectID
	backingHeader := file.header
		linkID, err := shared.NewObjectID()
	if err != nil {
		return response, fmt.Errorf("generate HardLink ObjectID: %w", err)
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

	required := int(geometry.Layout.NumChunks)
	if geometry.Layout.Type == distribution.LayoutParity {
		required++
	}
	placement, placementErr := layer.distribution.StripeIOIndexes(distribution.StripeIOIndexesRequest{
		ObjectID: file.objectID, StripeID: storage.StripeID(0),
	})
	indexes := placement.IOIndexes
	if placementErr != nil || len(indexes) != required {
		capacity, capacityErr := layer.distribution.GetCapacity(distribution.CapacityRequest{})
		if capacityErr != nil {
			return fmt.Errorf("read I/O capacities: %w", capacityErr)
		}
		indexes, err = selectIOIndexes(capacity.Capacities, required)
		if err != nil {
			return err
		}
	}
	_, err = layer.distribution.WriteStripe(distribution.WriteStripeRequest{
		Context: ctx, ObjectID: file.objectID, StripeID: storage.StripeID(0),
		ChunkSize: geometry.ChunkSize, Layout: geometry.Layout,
		IOIndexes: indexes, Data: entry.data,
	})
	if err != nil {
		return err
	}
	entry.state = stripeCacheClean
	file.stripeCache[0] = entry
	file.header = next
	return nil
}
