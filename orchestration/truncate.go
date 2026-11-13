package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

type TruncateRequest struct {
	Context  context.Context
	Handle   *FileHandle
	ObjectID shared.ObjectID
	Size     uint64
}
type TruncateResponse struct{}

func (layer *Orchestration) Truncate(request TruncateRequest) (TruncateResponse, error) {
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.truncateLocked(request)
}

func (layer *Orchestration) truncateLocked(request TruncateRequest) (TruncateResponse, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return TruncateResponse{}, err
	}
	if request.Handle == nil {
		return TruncateResponse{}, os.ErrInvalid
	}
	handle := request.Handle
	if handle.closed || handle.runtimeFile == nil {
		return TruncateResponse{}, os.ErrInvalid
	}
	if handle.accessMode == AccessModeReadOnly {
		return TruncateResponse{}, os.ErrPermission
	}
	object, exists := layer.objects[request.ObjectID]
	if !exists || (object.header.Deleted && (object.file == nil || object.file != handle.runtimeFile)) {
		return TruncateResponse{}, os.ErrNotExist
	}
	if (object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink) || object.file == nil || object.file != handle.runtimeFile {
		return TruncateResponse{}, fmt.Errorf("handle does not refer to writable file %s: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	file := handle.runtimeFile
	if request.Size == file.header.Size {
		return TruncateResponse{}, nil
	}
	if err := ctx.Err(); err != nil {
		return TruncateResponse{}, err
	}
	if err := layer.validateResizeCapacity(ctx, file.header, request.Size); err != nil {
		return TruncateResponse{}, err
	}
	if request.Size > uint64(int(^uint(0)>>1)) {
		return TruncateResponse{}, fmt.Errorf("truncate size exceeds addressable memory: %w", os.ErrInvalid)
	}
	next, err := updateHeader(file.header, request.Size, time.Now())
	if err != nil {
		return TruncateResponse{}, err
	}
	plan, err := planPlacementForResize(file.header, request.Size)
	if err != nil {
		return TruncateResponse{}, err
	}
	if len(plan) > 1 {
		next.Layout.ChunkSize = plan[1].ChunkSize
	} else {
		next.Layout.ChunkSize = cfg.MinimumChunkSize
	}
	staged := &runtimeFile{objectID: file.objectID, header: file.header, openCount: file.openCount, stripeCache: file.stripeCache, zeroStripeRanges: file.zeroStripeRanges}
	if err := layer.rebuildFileCacheFromStripes(ctx, staged, file.header, next, plan, ^uint64(0), nil); err != nil {
		return TruncateResponse{}, err
	}
	file.header = next
	file.stripeCache = staged.stripeCache
	file.zeroStripeRanges = staged.zeroStripeRanges
	return TruncateResponse{}, nil
}
