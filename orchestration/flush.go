package orchestration

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/storage"
)

type FlushRequest struct {
	Context context.Context
	Handle  *FileHandle
	Sync    bool
}
type FlushResponse struct{ PersistedStripes uint32 }

func (o *Orchestration) Flush(r FlushRequest) (FlushResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.flushLocked(r)
}

func (o *Orchestration) flushLocked(r FlushRequest) (FlushResponse, error) {
	var out FlushResponse
	ctx := r.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if r.Handle == nil {
		return out, os.ErrInvalid
	}
	h := r.Handle
	if h.closed || h.runtimeFile == nil {
		return out, os.ErrInvalid
	}
	f := h.runtimeFile
	ids := make([]uint64, 0)
	for id, entry := range f.stripeCache {
		if entry != nil && entry.state == stripeCacheDirty {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i] == 0 {
			return false
		}
		if ids[j] == 0 {
			return true
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		entry := f.stripeCache[id]
		if entry == nil || entry.state != stripeCacheDirty {
			continue
		}
		geometry, err := geometryFor(f.header, storage.StripeID(id))
		if err != nil {
			return out, err
		}
		required := int(geometry.Layout.NumChunks)
		if geometry.Layout.Type == distribution.LayoutParity {
			required++
		}
		placement, placementErr := o.distribution.StripeIOIndexes(distribution.StripeIOIndexesRequest{ObjectID: f.objectID, StripeID: storage.StripeID(id)})
		indexes := placement.IOIndexes
		if placementErr != nil || len(indexes) != required {
			capacity, capacityErr := o.distribution.GetCapacity(distribution.CapacityRequest{})
			if capacityErr != nil {
				return out, fmt.Errorf("read I/O capacities for stripe %d: %w", id, capacityErr)
			}
			indexes, err = selectIOIndexes(capacity.Capacities, required)
			if err != nil {
				return out, fmt.Errorf("select I/O devices for stripe %d: %w", id, err)
			}
		}
		_, err = o.distribution.WriteStripe(distribution.WriteStripeRequest{
			Context: ctx, ObjectID: f.objectID, StripeID: storage.StripeID(id),
			ChunkSize: geometry.ChunkSize, Layout: geometry.Layout,
			IOIndexes: indexes, Data: entry.data, Sync: r.Sync,
		})
		if err != nil {
			return out, fmt.Errorf("persist stripe %d: %w", id, err)
		}
		entry.state = stripeCacheClean
		out.PersistedStripes++
	}
	if r.Sync {
		if _, err := o.distribution.SyncObject(distribution.SyncObjectRequest{Context: ctx, ObjectID: f.objectID}); err != nil {
			return out, fmt.Errorf("sync object %s: %w", f.objectID.String(), err)
		}
	}
	return out, nil
}

type FsyncRequest struct {
	Context context.Context
	Handle  *FileHandle
}
type FsyncResponse struct{ PersistedStripes uint32 }

func (o *Orchestration) Fsync(r FsyncRequest) (FsyncResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fsyncLocked(r)
}

func (o *Orchestration) fsyncLocked(r FsyncRequest) (FsyncResponse, error) {
	result, err := o.flushLocked(FlushRequest{Context: r.Context, Handle: r.Handle, Sync: true})
	return FsyncResponse{PersistedStripes: result.PersistedStripes}, err
}
