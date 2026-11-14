package orchestration

import (
	"context"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

type SetAttrRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
	Handle   *FileHandle

	Mode     *uint32
	UID      *uint32
	GID      *uint32
	Size     *uint64
	Atime    *time.Time
	Mtime    *time.Time
	AtimeNow bool
	MtimeNow bool
}

type SetAttrResponse struct{}

// SetAttr updates logical attributes. A handle is optional, as FUSE may invoke
// setattr for path-based operations without an open file description.
func (layer *Orchestration) SetAttrLocked(request SetAttrRequest) (response SetAttrResponse, err error) {
	return layer.setAttrLocked(request)
}

func (layer *Orchestration) setAttrLocked(request SetAttrRequest) (response SetAttrResponse, err error) {
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
	if !exists {
		return response, os.ErrNotExist
	}
	if object.header.Deleted &&
		(request.Handle == nil || object.file == nil || openHandleCount(object.file) == 0) {
		return response, os.ErrNotExist
	}
	if request.Size != nil && object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, syscall.EINVAL
	}
	if request.Mode != nil && object.header.Kind == objectKindSymlink {
		return response, syscall.EPERM
	}

	var handle *FileHandle
	if request.Handle != nil {
		handle = request.Handle
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
		if err = ctx.Err(); err != nil {
			return response, err
		}

		// Apply changes to a private snapshot. A failed synchronous persistence
		// must not leak the requested attributes or partially rebuilt cache into
		// the live Runtime file.
		staged := &runtimeFile{
			objectID: file.objectID, header: file.header, openCount: file.openCount,
			stripeCache: make(map[uint64]*stripeCacheEntry),
		}
		next := file.header
		if request.Size != nil && *request.Size != next.Size {
			if err = layer.validateResizeCapacity(ctx, file.header, *request.Size); err != nil {
				return response, err
			}
			next, err = updateHeader(next, *request.Size, now)
			if err != nil {
				return response, err
			}
			plan, planErr := planPlacementForResize(file.header, *request.Size)
			if planErr != nil {
				return response, planErr
			}
			if len(plan) > 1 {
				next.Layout.ChunkSize = plan[1].ChunkSize
			} else {
				next.Layout.ChunkSize = cfg.MinimumChunkSize
			}
			if err = applyAttributeChanges(&next, request, now); err != nil {
				return response, err
			}
			staged.stripeCache = file.stripeCache
			if err = layer.rebuildFileCacheFromStripes(ctx, staged, file.header, next, plan, ^uint64(0), nil); err != nil {
				return response, err
			}
		} else {
			staged.stripeCache = cloneStripeCache(file.stripeCache)
			staged.zeroStripeRanges = cloneStripeRanges(file.zeroStripeRanges)
			if err = applyAttributeChanges(&next, request, now); err != nil {
				return response, err
			}
			if next != file.header {
				if err = layer.updateHeaderStripeLocked(ctx, staged, next); err != nil {
					return response, err
				}
			}
		}

		staged.header = next
		if handle == nil {
			if _, err = layer.persistDirtyStripesLocked(ctx, staged); err != nil {
				return response, err
			}
		}
		file.header = staged.header
		file.stripeCache = staged.stripeCache
		file.zeroStripeRanges = staged.zeroStripeRanges
		if layer.headers == nil {
			layer.headers = make(map[shared.ObjectID]header)
		}
		layer.headers[file.objectID] = staged.header
		return response, nil
	}

	// Namespace-only objects have no shared data Runtime. Persist their updated
	// Header before publishing it in memory.
	next := object.header
	if request.Size != nil && *request.Size != next.Size {
		return response, syscall.EINVAL
	}
	if err = applyAttributeChanges(&next, request, now); err != nil {
		return response, err
	}
	if next == object.header {
		return response, nil
	}
	temporary := &runtimeFile{
		objectID:    request.ObjectID,
		header:      next,
		stripeCache: make(map[uint64]*stripeCacheEntry),
	}
	if err = layer.updateHeaderStripeLocked(ctx, temporary, next); err != nil {
		return response, err
	}
	if _, err = layer.persistDirtyStripesLocked(ctx, temporary); err != nil {
		return response, err
	}
	object.header = next
	if layer.headers == nil {
		layer.headers = make(map[shared.ObjectID]header)
	}
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
	if len(entry.data) < int(cfg.HeaderReservedSize) {
		entry.data = append(entry.data, make([]byte, int(cfg.HeaderReservedSize)-len(entry.data))...)
	}
	copy(entry.data[:len(headerBytes)], headerBytes)
	entry.state = stripeCacheDirty
	file.stripeCache[0] = entry
	return nil
}

// persistDirtyStripesLocked persists the dirty cache entries in StripeID order.
func (layer *Orchestration) persistDirtyStripesLocked(ctx context.Context, file *runtimeFile, syncToDisk ...bool) (uint32, error) {
	sync := len(syncToDisk) > 0 && syncToDisk[0]
	work := append([]stripeRange(nil), file.zeroStripeRanges...)
	for id, entry := range file.stripeCache {
		if entry != nil && entry.state == stripeCacheDirty {
			work = append(work, stripeRange{start: id, end: id})
		}
	}
	if len(work) == 0 {
		return 0, nil
	}
	sort.Slice(work, func(i, j int) bool { return work[i].start < work[j].start })
	merged := make([]stripeRange, 0, len(work))
	for _, item := range work {
		if len(merged) == 0 {
			merged = append(merged, item)
			continue
		}
		last := &merged[len(merged)-1]
		if item.start <= last.end || (last.end != ^uint64(0) && item.start == last.end+1) {
			if item.end > last.end {
				last.end = item.end
			}
			continue
		}
		merged = append(merged, item)
	}

	// Persist only dirty stripes and zero ranges, in StripeID order. Stripe 0
	// therefore remains the first commit and the zero payload stays O(1) in RAM.
	var zeroData []byte
	var persisted uint32
	for _, item := range merged {
		for id := item.start; ; id++ {
			if err := ctx.Err(); err != nil {
				return persisted, err
			}
			entry := file.stripeCache[id]
			isZero := containsStripeRange(file.zeroStripeRanges, id)
			geometry, err := geometryFor(file.header, storage.StripeID(id))
			if err != nil {
				return persisted, err
			}
			data := []byte(nil)
			if entry != nil && entry.state == stripeCacheDirty {
				data = entry.data
			} else if isZero {
				if zeroData == nil || uint64(len(zeroData)) != geometry.Capacity {
					zeroData = make([]byte, int(geometry.Capacity))
				}
				data = zeroData
			} else {
				if id == item.end {
					break
				}
				continue
			}
			required := int(geometry.Layout.NumChunks)
			if geometry.Layout.Type == distribution.LayoutParity {
				required++
			}
			placement, placementErr := layer.distribution.StripeIOIndexes(distribution.StripeIOIndexesRequest{ObjectID: file.objectID, StripeID: storage.StripeID(id)})
			indexes := placement.IOIndexes
			if placementErr != nil || len(indexes) != required {
				capacity, capacityErr := layer.distribution.GetCapacity(distribution.CapacityRequest{})
				if capacityErr != nil {
					return persisted, fmt.Errorf("read I/O capacities for stripe %d: %w", id, capacityErr)
				}
				indexes, err = selectIOIndexes(capacity.Capacities, required)
				if err != nil {
					return persisted, fmt.Errorf("select I/O devices for stripe %d: %w", id, err)
				}
			}
			if _, err = layer.distribution.WriteStripe(distribution.WriteStripeRequest{
				Context: ctx, ObjectID: file.objectID, StripeID: storage.StripeID(id),
				ChunkSize: geometry.ChunkSize, Layout: geometry.Layout,
				IOIndexes: indexes, Data: data, Sync: sync,
			}); err != nil {
				return persisted, fmt.Errorf("persist stripe %d: %w", id, err)
			}
			if entry != nil {
				entry.state = stripeCacheClean
			}
			if isZero {
				file.zeroStripeRanges = removeStripeFromRanges(file.zeroStripeRanges, id)
			}
			persisted++
			if id == item.end {
				break
			}
		}
	}
	return persisted, nil
}

func sortStripeIDs(ids []uint64) {
	// Insertion sort keeps this helper dependency-free for the tiny dirty set.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
