package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type SetAttrRequest struct {
	Context context.Context
	ObjectID shared.ObjectID
	Handle *FileHandle

	Mode *uint32
	UID *uint32
	GID *uint32
	Size *uint64
	Atime *time.Time
	Mtime *time.Time
	AtimeNow bool
	MtimeNow bool
}

type SetAttrResponse struct{}

// SetAttr updates logical attributes. A handle is optional, as FUSE may invoke
// setattr for path-based operations without an open file description.
func (layer *Orchestration) SetAttr(request SetAttrRequest) (response SetAttrResponse, err error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if (request.Atime != nil && request.AtimeNow) || (request.Mtime != nil && request.MtimeNow) {
		return response, os.ErrInvalid
	}

	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted || object.header.Kind == objectKindHardLinkedFile {
		return response, os.ErrNotExist
	}
	if request.Size != nil && object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, fmt.Errorf("object %s does not support size changes: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if request.Mode != nil && object.header.Kind == objectKindSymlink {
		return response, os.ErrPermission
	}

	var handle *FileHandle
	if request.Handle != nil {
		handle = request.Handle
		handle.mu.Lock()
		defer handle.mu.Unlock()
		if handle.closed || handle.runtimeFile == nil {
			return response, os.ErrInvalid
		}
		if handle.accessMode == AccessModeReadOnly && request.Size != nil {
			return response, os.ErrPermission
		}
		if object.file == nil || object.file != handle.runtimeFile {
			return response, fmt.Errorf("handle does not refer to object %s: %w", request.ObjectID.String(), os.ErrInvalid)
		}
	}

	now := time.Now()
	if object.file != nil {
		file := object.file
		file.mu.Lock()
		defer file.mu.Unlock()
		if err = ctx.Err(); err != nil {
			return response, err
		}
		next := file.header
		if request.Size != nil && *request.Size != next.Size {
			if *request.Size > uint64(int(^uint(0)>>1)) {
				return response, fmt.Errorf("setattr size exceeds addressable memory: %w", os.ErrInvalid)
			}
			next, err = updateHeader(next, *request.Size, now)
			if err != nil {
				return response, err
			}
			plan, planErr := planPlacement(*request.Size)
			if planErr != nil {
				return response, planErr
			}
			if len(plan) > 1 {
				next.Layout.ChunkSize = plan[1].ChunkSize
			}
			if err = applyAttributeChanges(&next, request, now); err != nil {
				return response, err
			}
			if err = layer.rebuildFileCacheFromStripes(ctx, file, file.header, next, plan, ^uint64(0), nil); err != nil {
				return response, err
			}
		} else {
			if err = applyAttributeChanges(&next, request, now); err != nil {
				return response, err
			}
			if next == file.header {
				return response, nil
			}
			if err = layer.updateHeaderStripeLocked(ctx, file, next); err != nil {
				return response, err
			}
		}
		file.header = next
		layer.headers[file.objectID] = next
		if handle == nil {
			if err = layer.persistDirtyStripesLocked(ctx, file); err != nil {
				return response, err
			}
		}
		return response, nil
	}

	// Directories and symlinks have no runtime file handle. Use a temporary
	// stripe cache to update and synchronously persist their Header stripe.
	next := object.header
	if request.Size != nil && *request.Size != next.Size {
		return response, fmt.Errorf("object %s does not support size changes: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if err = applyAttributeChanges(&next, request, now); err != nil {
		return response, err
	}
	if next == object.header {
		return response, nil
	}
	temporary := &runtimeFile{
		objectID: request.ObjectID,
		header: next,
		stripeCache: make(map[uint64]*stripeCacheEntry),
	}
	if err = layer.updateHeaderStripeLocked(ctx, temporary, next); err != nil {
		return response, err
	}
	if err = layer.persistDirtyStripesLocked(ctx, temporary); err != nil {
		return response, err
	}
	object.header = next
	layer.headers[request.ObjectID] = next
	return response, nil
}

func applyAttributeChanges(next *header, request SetAttrRequest, now time.Time) error {
	changed := false
	if request.Mode != nil && next.Mode != *request.Mode {
		next.Mode = *request.Mode
		changed = true
	}
	if request.UID != nil && next.UID != *request.UID {
		next.UID = *request.UID
		changed = true
	}
	if request.GID != nil && next.GID != *request.GID {
		next.GID = *request.GID
		changed = true
	}
	if request.Atime != nil && !next.Atime.Equal(*request.Atime) {
		next.Atime = *request.Atime
		changed = true
	} else if request.AtimeNow {
		next.Atime = now
		changed = true
	}
	if request.Mtime != nil && !next.Mtime.Equal(*request.Mtime) {
		next.Mtime = *request.Mtime
		changed = true
	} else if request.MtimeNow {
		next.Mtime = now
		changed = true
	}
	if changed {
		next.Ctime = now
	}
	return nil
}

func (layer *Orchestration) updateHeaderStripeLocked(ctx context.Context, file *runtimeFile, next header) error {
	headerBytes, err := next.MarshalBinary()
	if err != nil {
		return err
	}
	if file.stripeCache == nil {
		file.stripeCache = make(map[uint64]*stripeCacheEntry)
	}
	geometry, err := geometryFor(next, storage.StripeID(0))
	if err != nil {
		return err
	}
	entry, err := layer.loadStripeForWriteLocked(ctx, file, geometry)
	if err != nil {
		return err
	}
	if len(entry.data) < int(cfgHeaderReservedSize()) {
		entry.data = append(entry.data, make([]byte, int(cfgHeaderReservedSize())-len(entry.data))...)
	}
	copy(entry.data[:len(headerBytes)], headerBytes)
	entry.state = stripeCacheDirty
	file.stripeCache[0] = entry
	return nil
}

// persistDirtyStripesLocked persists the dirty cache entries in StripeID order.
func (layer *Orchestration) persistDirtyStripesLocked(ctx context.Context, file *runtimeFile) error {
	ids := make([]uint64, 0, len(file.stripeCache))
	for id, entry := range file.stripeCache {
		if entry != nil && entry.state == stripeCacheDirty {
			ids = append(ids, id)
		}
	}
	// Stripe 0 must be committed first because it contains the authoritative Header.
	sortStripeIDs(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := file.stripeCache[id]
		if entry == nil || entry.state != stripeCacheDirty {
			continue
		}
		geometry, err := geometryFor(file.header, storage.StripeID(id))
		if err != nil {
			return err
		}
		required := int(geometry.Layout.NumChunks)
		if geometry.Layout.Type == distribution.LayoutParity {
			required++
		}
		placement, placementErr := layer.distribution.StripeIOIndexes(distribution.StripeIOIndexesRequest{ObjectID: file.objectID, StripeID: storage.StripeID(id)})
		indexes := placement.IOIndexes
		if placementErr != nil || len(indexes) != required {
			available, capacityErr := layer.distribution.AvailableIOBytes(distribution.AvailableIOBytesRequest{})
			if capacityErr != nil {
				return fmt.Errorf("read I/O capacities for stripe %d: %w", id, capacityErr)
			}
			indexes, err = selectIOIndexes(available.Bytes, required)
			if err != nil {
				return fmt.Errorf("select I/O devices for stripe %d: %w", id, err)
			}
		}
		if _, err = layer.distribution.WriteStripe(distribution.WriteStripeRequest{
			Context: ctx, ObjectID: file.objectID, StripeID: storage.StripeID(id),
			ChunkSize: geometry.ChunkSize, Layout: geometry.Layout,
			IOIndexes: indexes, Data: entry.data,
		}); err != nil {
			return fmt.Errorf("persist stripe %d: %w", id, err)
		}
		entry.state = stripeCacheClean
	}
	return nil
}

func sortStripeIDs(ids []uint64) {
	// Insertion sort keeps this helper dependency-free for the tiny dirty set.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
