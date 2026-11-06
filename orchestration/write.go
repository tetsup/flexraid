package orchestration

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

type WriteRequest struct {
	Context  context.Context
	Handle   *FileHandle
	ObjectID shared.ObjectID
	Offset   uint64
	Data     []byte
}

type WriteResponse struct {
	Written uint32
}

// Write updates logical file content in the shared stripe cache.
func (layer *Orchestration) Write(request WriteRequest) (response WriteResponse, err error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if request.Handle == nil {
		return response, os.ErrInvalid
	}
	if uint64(len(request.Data)) > uint64(^uint32(0)) {
		return response, fmt.Errorf("write request exceeds maximum size: %w", os.ErrInvalid)
	}
	handle := request.Handle
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed || handle.runtimeFile == nil {
		return response, os.ErrInvalid
	}
	if handle.accessMode == AccessModeReadOnly {
		return response, os.ErrPermission
	}
	if handle.syncMode != SyncModeNone {
		return response, fmt.Errorf("synchronous writes require persistence support: %w", syscall.ENOTSUP)
	}
	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return response, os.ErrNotExist
	}
	if (object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink) || object.file == nil || object.file != handle.runtimeFile {
		return response, fmt.Errorf("handle does not refer to writable file %s: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if len(request.Data) == 0 {
		return response, nil
	}

	file := handle.runtimeFile
	file.mu.Lock()
	defer file.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if file.stripeCache == nil {
		file.stripeCache = make(map[uint64]*stripeCacheEntry)
	}
	oldHeader := file.header
	writeOffset := request.Offset
	if handle.append {
		writeOffset = oldHeader.Size
	}
	writeEnd := writeOffset + uint64(len(request.Data))
	if writeEnd < writeOffset {
		return response, os.ErrInvalid
	}
	newSize := oldHeader.Size
	if writeEnd > newSize {
		newSize = writeEnd
	}

	if newSize != oldHeader.Size {
		oldData, readErr := layer.readFileDataLocked(ctx, file, oldHeader)
		if readErr != nil {
			return response, fmt.Errorf("read existing file before placement change: %w", readErr)
		}
		if writeEnd > uint64(len(oldData)) {
			oldData = append(oldData, make([]byte, int(writeEnd)-len(oldData))...)
		}
		copy(oldData[int(writeOffset):int(writeEnd)], request.Data)
		nextHeader, headerErr := updateHeader(oldHeader, newSize, time.Now())
		if headerErr != nil {
			return response, headerErr
		}
		plan, planErr := planPlacement(newSize)
		if planErr != nil {
			return response, planErr
		}
		if len(plan) > 1 {
			nextHeader.Layout.ChunkSize = plan[1].ChunkSize
		} else {
			nextHeader.Layout.ChunkSize = cfg.MinimumChunkSize
		}
		if err = layer.rebuildFileCache(ctx, file, nextHeader, oldData, plan); err != nil {
			return response, err
		}
		file.header = nextHeader
		response.Written = uint32(len(request.Data))
		return response, nil
	}

	// A size-preserving overwrite keeps the established placement and only dirties touched stripes.
	nextHeader := oldHeader
	nextHeader.Mtime, nextHeader.Ctime = time.Now(), time.Now()
	geometry, local, locateErr := locateFileOffset(nextHeader, writeOffset)
	if locateErr != nil {
		return response, locateErr
	}
	remaining := request.Data
	dirty := make(map[uint64]*stripeCacheEntry)
	for len(remaining) > 0 {
		g := geometry
		entry, loadErr := layer.loadStripeForWriteLocked(ctx, file, g)
		if loadErr != nil {
			return response, loadErr
		}
		localOffset := local
		if g.StripeID != 0 {
			localOffset = local
		}
		count := uint64(len(remaining))
		available := g.Capacity - (localOffset - g.DataOffset)
		if count > available {
			count = available
		}
		end := localOffset + count
		if uint64(len(entry.data)) < end {
			entry.data = append(entry.data, make([]byte, int(end)-len(entry.data))...)
		}
		copy(entry.data[int(localOffset):int(end)], remaining[:int(count)])
		dirty[uint64(g.StripeID)] = entry
		remaining = remaining[int(count):]
		writeOffset += count
		if len(remaining) > 0 {
			geometry, local, locateErr = locateFileOffset(nextHeader, writeOffset)
			if locateErr != nil {
				return response, locateErr
			}
		}
	}
	headerBytes, marshalErr := nextHeader.MarshalBinary()
	if marshalErr != nil {
		return response, marshalErr
	}
	headerEntry := dirty[0]
	if headerEntry == nil {
		var loadErr error
		headerEntry, loadErr = layer.loadStripeForWriteLocked(ctx, file, mustGeometry(nextHeader, 0))
		if loadErr != nil {
			return response, loadErr
		}
	}
	if len(headerEntry.data) < int(cfg.HeaderReservedSize) {
		headerEntry.data = append(headerEntry.data, make([]byte, int(cfg.HeaderReservedSize)-len(headerEntry.data))...)
	}
	copy(headerEntry.data[:len(headerBytes)], headerBytes)
	dirty[0] = headerEntry
	for id, entry := range dirty {
		entry.state = stripeCacheDirty
		file.stripeCache[id] = entry
	}
	file.header = nextHeader
	response.Written = uint32(len(request.Data))
	return response, nil
}

func mustGeometry(h header, id storage.StripeID) stripeGeometry {
	g, _ := geometryFor(h, id)
	return g
}

func (layer *Orchestration) loadStripeForWriteLocked(ctx context.Context, file *runtimeFile, g stripeGeometry) (*stripeCacheEntry, error) {
	if cached := file.stripeCache[uint64(g.StripeID)]; cached != nil {
		return &stripeCacheEntry{data: append([]byte(nil), cached.data...), state: cached.state}, nil
	}
	stripe, err := layer.distribution.ReadStripe(distribution.ReadStripeRequest{Context: ctx, ObjectID: file.objectID, StripeID: g.StripeID, ChunkSize: g.ChunkSize})
	if err != nil {
		return nil, err
	}
	return &stripeCacheEntry{data: append([]byte(nil), stripe.Data...), state: stripeCacheClean}, nil
}

func (layer *Orchestration) readFileDataLocked(ctx context.Context, file *runtimeFile, h header) ([]byte, error) {
	result := make([]byte, 0, int(h.Size))
	for offset := uint64(0); offset < h.Size; {
		g, local, err := locateFileOffset(h, offset)
		if err != nil {
			return nil, err
		}
		var data []byte
		if cached := file.stripeCache[uint64(g.StripeID)]; cached != nil {
			data = cached.data
		} else {
			stripe, readErr := layer.distribution.ReadStripe(distribution.ReadStripeRequest{Context: ctx, ObjectID: file.objectID, StripeID: g.StripeID, ChunkSize: g.ChunkSize})
			if readErr != nil {
				return nil, readErr
			}
			data = stripe.Data
		}
		count := g.Capacity - (local - g.DataOffset)
		if remain := h.Size - offset; count > remain {
			count = remain
		}
		if local+count > uint64(len(data)) {
			return nil, distribution.ErrDataCorrupted
		}
		result = append(result, data[int(local):int(local+count)]...)
		offset += count
	}
	return result, nil
}

func (layer *Orchestration) rebuildFileCache(ctx context.Context, file *runtimeFile, h header, data []byte, plan []stripePlacement) error {
	headerBytes, err := h.MarshalBinary()
	if err != nil {
		return err
	}
	cache := make(map[uint64]*stripeCacheEntry, len(plan))
	for _, p := range plan {
		if err := ctx.Err(); err != nil {
			return err
		}
		var stripeData []byte
		if p.StripeID == 0 {
			stripeData = make([]byte, int(cfg.HeaderReservedSize)+int(p.DataSize))
			copy(stripeData, headerBytes)
			copy(stripeData[int(cfg.HeaderReservedSize):], data[:int(p.DataSize)])
		} else {
			start := plan[0].DataSize
			for i := 1; i < int(p.StripeID); i++ {
				start += plan[i].DataSize
			}
			stripeData = append([]byte(nil), data[int(start):int(start+p.DataSize)]...)
		}
		cache[p.StripeID] = &stripeCacheEntry{data: stripeData, state: stripeCacheDirty}
	}
	file.stripeCache = cache
	return nil
}
