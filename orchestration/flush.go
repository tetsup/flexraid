package orchestration

import (
	"context"
	"fmt"
	"os"

	"github.com/tetsup/flexraid/distribution"
)

type FlushRequest struct {
	Context context.Context
	Handle  *FileHandle
	Sync    bool
}
type FlushResponse struct{ PersistedStripes uint32 }

func (o *Orchestration) FlushLocked(r FlushRequest) (FlushResponse, error) {
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
	persisted, err := o.persistDirtyStripesLocked(ctx, f, r.Sync)
	if err != nil {
		return out, err
	}
	out.PersistedStripes = persisted
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

func (o *Orchestration) FsyncLocked(r FsyncRequest) (FsyncResponse, error) {
	return o.fsyncLocked(r)
}

func (o *Orchestration) fsyncLocked(r FsyncRequest) (FsyncResponse, error) {
	result, err := o.flushLocked(FlushRequest{Context: r.Context, Handle: r.Handle, Sync: true})
	return FsyncResponse{PersistedStripes: result.PersistedStripes}, err
}
