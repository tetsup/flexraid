package orchestration

import (
	"context"
	"fmt"
	"github.com/tetsup/flexraid/distribution"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
	"os"
	"sort"
)

type FlushRequest struct {
	Context   context.Context
	Handle    *FileHandle
	Placement *cfg.Placement
}
type FlushResponse struct{ PersistedStripes uint32 }

func (o *Orchestration) Flush(r FlushRequest) (FlushResponse, error) {
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
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.runtimeFile == nil {
		return out, os.ErrInvalid
	}
	f := h.runtimeFile
	f.mu.Lock()
	defer f.mu.Unlock()
	p := cfg.DefaultPlacement()
	if r.Placement != nil {
		p = *r.Placement
	}
	if err := validatePersistencePlacement(p); err != nil {
		return out, err
	}
	indexes := make([]int, p.DiskCount)
	for i := range indexes {
		indexes[i] = i
	}
	ids := make([]uint64, 0)
	for id, e := range f.stripeCache {
		if e != nil && e.state == stripeCacheDirty {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	l, err := distributionLayout(f.header.Layout)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		e := f.stripeCache[id]
		if e == nil || e.state != stripeCacheDirty {
			continue
		}
		_, err := o.distribution.WriteStripe(distribution.WriteStripeRequest{Context: ctx, ObjectID: f.objectID, StripeID: storage.StripeID(id), ChunkSize: f.header.Layout.ChunkSize, Layout: l, IOIndexes: indexes, Data: e.data})
		if err != nil {
			return out, fmt.Errorf("persist stripe %d: %w", id, err)
		}
		e.state = stripeCacheClean
		out.PersistedStripes++
	}
	return out, nil
}

type FsyncRequest struct {
	Context   context.Context
	Handle    *FileHandle
	Placement *cfg.Placement
}
type FsyncResponse struct{ PersistedStripes uint32 }

func (o *Orchestration) Fsync(r FsyncRequest) (FsyncResponse, error) {
	v, e := o.Flush(FlushRequest{Context: r.Context, Handle: r.Handle, Placement: r.Placement})
	return FsyncResponse{PersistedStripes: v.PersistedStripes}, e
}

func validatePersistencePlacement(p cfg.Placement) error {
	if p.DiskCount < 1 || p.DiskCount > 9 || p.DataChunks < 1 || p.DataChunks > 8 || p.ParityChunks != 1 || p.DiskCount < p.DataChunks+p.ParityChunks {
		return fmt.Errorf("invalid placement layout")
	}
	if p.MaxChunkSize < cfg.MinimumChunkSize || p.MaxChunkSize%cfg.MinimumChunkSize != 0 {
		return fmt.Errorf("invalid maximum chunk size")
	}
	return nil
}
func distributionLayout(v layout) (distribution.Layout, error) {
	switch v.Mode {
	case layoutModeMirror:
		if v.DataChunks == 0 || v.DataChunks > 9 || v.ParityChunks != 0 {
			return distribution.Layout{}, errInvalidHeader
		}
		return distribution.Layout{Type: distribution.LayoutMirror, NumChunks: uint8(v.DataChunks)}, nil
	case layoutModeStripe:
		if v.DataChunks == 0 || v.DataChunks > 8 || v.ParityChunks != 1 {
			return distribution.Layout{}, errInvalidHeader
		}
		return distribution.Layout{Type: distribution.LayoutParity, NumChunks: uint8(v.DataChunks)}, nil
	default:
		return distribution.Layout{}, errInvalidHeader
	}
}
