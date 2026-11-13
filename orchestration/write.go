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
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.writeLocked(request)
}

func (layer *Orchestration) writeLocked(request WriteRequest) (response WriteResponse, err error) {
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
	if handle.closed || handle.runtimeFile == nil {
		return response, os.ErrInvalid
	}
	if handle.accessMode == AccessModeReadOnly {
		return response, os.ErrPermission
	}

	object, exists := layer.objects[request.ObjectID]
	if !exists {
		return response, os.ErrNotExist
	}
	if (object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink) ||
		object.file == nil || object.file != handle.runtimeFile {
		return response, fmt.Errorf("handle does not refer to writable file %s: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if len(request.Data) == 0 {
		return response, nil
	}

	file := handle.runtimeFile
	if err = ctx.Err(); err != nil {
		return response, err
	}

	// Build changes in a private cache. Resize rebuilds only the destination
	// stripes and must not clone the old cache first.
	staged := &runtimeFile{
		objectID: file.objectID, header: file.header, openCount: file.openCount,
		stripeCache: make(map[uint64]*stripeCacheEntry),
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

	if newSize == oldHeader.Size {
		staged.stripeCache = cloneStripeCache(file.stripeCache)
		staged.zeroStripeRanges = cloneStripeRanges(file.zeroStripeRanges)
	}
	if newSize != oldHeader.Size {
		nextHeader, headerErr := updateHeader(oldHeader, newSize, time.Now())
		if headerErr != nil {
			return response, headerErr
		}
		plan, planErr := planPlacementForResize(oldHeader, newSize)
		if planErr != nil {
			return response, planErr
		}
		if len(plan) > 1 {
			nextHeader.Layout.ChunkSize = plan[1].ChunkSize
		} else {
			nextHeader.Layout.ChunkSize = cfg.MinimumChunkSize
		}
		// The rebuild reads old cached stripes but never mutates them. Share the
		// old map until the complete destination cache is ready.
		staged.stripeCache = file.stripeCache
		if err = layer.rebuildFileCacheFromStripes(ctx, staged, oldHeader, nextHeader, plan, writeOffset, request.Data); err != nil {
			return response, err
		}
		staged.header = nextHeader
	} else {
		nextHeader := oldHeader
		now := time.Now()
		nextHeader.Mtime, nextHeader.Ctime = now, now
		geometry, local, locateErr := locateFileOffset(nextHeader, writeOffset)
		if locateErr != nil {
			return response, locateErr
		}
		remaining := request.Data
		dirty := make(map[uint64]*stripeCacheEntry)
		for len(remaining) > 0 {
			g := geometry
			entry, loadErr := layer.loadStripeForWriteLocked(ctx, staged, g)
			if loadErr != nil {
				return response, loadErr
			}
			localOffset := local
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
			headerEntry, loadErr = layer.loadStripeForWriteLocked(ctx, staged, mustGeometry(nextHeader, 0))
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
			staged.stripeCache[id] = entry
		}
		staged.header = nextHeader
	}

	if handle.syncMode != SyncModeNone {
		if err = layer.persistDirtyStripesLocked(ctx, staged, true); err != nil {
			return response, err
		}
		if _, err = layer.distribution.SyncObject(distribution.SyncObjectRequest{Context: ctx, ObjectID: file.objectID}); err != nil {
			return response, fmt.Errorf("sync object %s: %w", file.objectID.String(), err)
		}
	}
	file.header = staged.header
	file.stripeCache = staged.stripeCache
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
	if containsStripeRange(file.zeroStripeRanges, uint64(g.StripeID)) {
		file.zeroStripeRanges = removeStripeFromRanges(file.zeroStripeRanges, uint64(g.StripeID))
		return &stripeCacheEntry{data: make([]byte, int(g.Capacity)), state: stripeCacheDirty}, nil
	}
	data, err := layer.readStripeValidated(ctx, file.objectID, file.header, g)
	if err != nil {
		return nil, err
	}
	return &stripeCacheEntry{data: append([]byte(nil), data...), state: stripeCacheClean}, nil
}

// rebuildFileCacheFromStripes maps old logical ranges to the new stripe geometry.
// It never assembles the whole file into one byte slice; each source and destination
// operation is bounded by an individual stripe.
func (layer *Orchestration) rebuildFileCacheFromStripes(
	ctx context.Context,
	file *runtimeFile,
	oldHeader, newHeader header,
	plan []stripePlacement,
	writeOffset uint64,
	writeData []byte,
) error {
	headerBytes, err := newHeader.MarshalBinary()
	if err != nil {
		return err
	}
	cache := make(map[uint64]*stripeCacheEntry)
	var zeroRanges []stripeRange
	var destinationOffset uint64
	for _, placement := range plan {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix := uint64(0)
		if placement.StripeID == 0 {
			prefix = cfg.HeaderReservedSize
		}
		if placement.DataSize > uint64(int(^uint(0)>>1))-prefix {
			return fmt.Errorf("stripe %d exceeds addressable memory: %w", placement.StripeID, os.ErrInvalid)
		}
		// Preserve only the old file range that overlaps this destination stripe.
		destinationEnd := destinationOffset + placement.DataSize
		writeEnd := writeOffset + uint64(len(writeData))
		overlapStart := destinationOffset
		if writeOffset > overlapStart {
			overlapStart = writeOffset
		}
		overlapEnd := destinationEnd
		if writeEnd < overlapEnd {
			overlapEnd = writeEnd
		}
		hasWrite := overlapStart < overlapEnd

		// If this regular stripe is wholly inside the old EOF, its geometry is
		// unchanged, and no caller data touches it, retain its existing version.
		// This keeps ordinary growth/shrink operations limited to changed stripes.
		if placement.StripeID > 0 && destinationEnd <= oldHeader.Size && !hasWrite {
			oldGeometry, geometryErr := geometryFor(oldHeader, storage.StripeID(placement.StripeID))
			newLayout, layoutErr := distributionLayout(placement.Layout)
			if geometryErr == nil && layoutErr == nil &&
				oldGeometry.FileOffset == destinationOffset &&
				oldGeometry.ChunkSize == placement.ChunkSize &&
				oldGeometry.Layout == newLayout {
				if containsStripeRange(file.zeroStripeRanges, placement.StripeID) {
					zeroRanges = appendStripeRange(zeroRanges, placement.StripeID)
				} else if existing := file.stripeCache[placement.StripeID]; existing != nil {
					cache[placement.StripeID] = existing
				}
				destinationOffset += placement.DataSize
				continue
			}
		}
		preserveEnd := destinationEnd
		if preserveEnd > oldHeader.Size {
			preserveEnd = oldHeader.Size
		}
		var preserved []byte
		if destinationOffset < preserveEnd {
			var readErr error
			preserved, readErr = layer.readLogicalRangeLocked(ctx, file, oldHeader, destinationOffset, preserveEnd-destinationOffset)
			if readErr != nil {
				return fmt.Errorf("read source range for stripe %d: %w", placement.StripeID, readErr)
			}
		}

		// Apply the caller's write only to the overlapping destination stripe.
		// Fully zero-filled regular stripes share one immutable zero buffer.
		// This keeps payload memory bounded even when a write creates a huge gap.
		if placement.StripeID > 0 && len(preserved) == 0 && !hasWrite {
			zeroRanges = appendStripeRange(zeroRanges, placement.StripeID)
			destinationOffset += placement.DataSize
			continue
		}
		stripeData := make([]byte, int(prefix+placement.DataSize))
		if placement.StripeID == 0 {
			copy(stripeData, headerBytes)
		}
		if len(preserved) > 0 {
			copy(stripeData[int(prefix):], preserved)
		}
		if hasWrite {
			srcStart := overlapStart - writeOffset
			dstStart := prefix + overlapStart - destinationOffset
			copy(stripeData[int(dstStart):int(dstStart+overlapEnd-overlapStart)], writeData[int(srcStart):int(srcStart+overlapEnd-overlapStart)])
		}
		cache[placement.StripeID] = &stripeCacheEntry{data: stripeData, state: stripeCacheDirty}
		destinationOffset += placement.DataSize
	}
	file.stripeCache = cache
	file.zeroStripeRanges = zeroRanges
	return nil
}

func (layer *Orchestration) readLogicalRangeLocked(
	ctx context.Context,
	file *runtimeFile,
	h header,
	offset, size uint64,
) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	if size > uint64(int(^uint(0)>>1)) {
		return nil, os.ErrInvalid
	}
	result := make([]byte, 0, int(size))
	end := offset + size
	if end < offset || end > h.Size {
		return nil, os.ErrInvalid
	}
	for offset < end {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		geometry, local, err := locateFileOffset(h, offset)
		if err != nil {
			return nil, err
		}
		entry := file.stripeCache[uint64(geometry.StripeID)]
		var data []byte
		if entry != nil {
			data = entry.data
		} else if containsStripeRange(file.zeroStripeRanges, uint64(geometry.StripeID)) {
			data = make([]byte, int(geometry.Capacity))
		} else {
			readData, readErr := layer.readStripeValidated(ctx, file.objectID, h, geometry)
			if readErr != nil {
				return nil, readErr
			}
			data = readData
		}
		if local >= uint64(len(data)) {
			return nil, distribution.ErrDataCorrupted
		}
		available := geometry.Capacity - (local - geometry.DataOffset)
		if actual := uint64(len(data)) - local; available > actual {
			available = actual
		}
		count := end - offset
		if count > available {
			count = available
		}
		if count == 0 {
			return nil, distribution.ErrDataCorrupted
		}
		result = append(result, data[int(local):int(local+count)]...)
		offset += count
	}
	return result, nil
}

func cloneStripeCache(source map[uint64]*stripeCacheEntry) map[uint64]*stripeCacheEntry {
	clone := make(map[uint64]*stripeCacheEntry, len(source))
	for id, entry := range source {
		if entry == nil {
			clone[id] = nil
			continue
		}
		clone[id] = &stripeCacheEntry{
			data:  append([]byte(nil), entry.data...),
			state: entry.state,
		}
	}
	return clone
}
