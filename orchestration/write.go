package orchestration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
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

// Write updates the shared stripe cache. Persistence is performed by Flush/Fsync.
// Offset is relative to logical file data and never includes the internal Header.
func (layer *Orchestration) Write(request WriteRequest) (response WriteResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if layer.logger != nil {
			layer.logger.Debug("Orchestration Write completed",
				"object_id", request.ObjectID.String(),
				"offset", request.Offset,
				"requested_bytes", len(request.Data),
				"written_bytes", response.Written,
				"duration", time.Since(started),
				"error", err)
		}
	}()

	if err = ctx.Err(); err != nil {
		return response, err
	}
	if request.Handle == nil {
		return response, os.ErrInvalid
	}
	if uint64(len(request.Data)) > math.MaxUint32 {
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
	if object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, fmt.Errorf("object %s is not a writable regular file: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if object.file == nil || object.file != handle.runtimeFile {
		return response, fmt.Errorf("handle does not refer to object %s: %w", request.ObjectID.String(), os.ErrInvalid)
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

	fileHeader := file.header
	writeOffset := request.Offset
	if handle.append {
		// The file lock makes choosing EOF and updating it atomic across handles.
		writeOffset = fileHeader.Size
	}
	writeEnd := writeOffset + uint64(len(request.Data))
	if writeEnd < writeOffset || writeEnd > math.MaxUint64-uint64(headerSize) {
		return response, fmt.Errorf("write range overflows logical address space: %w", os.ErrInvalid)
	}
	if fileHeader.Layout.ChunkSize == 0 {
		return response, fmt.Errorf("%w: zero chunk size", errInvalidHeader)
	}

	stripeCapacity := uint64(fileHeader.Layout.ChunkSize)
	var layout distribution.Layout
	switch fileHeader.Layout.Mode {
	case layoutModeMirror:
		if fileHeader.Layout.ParityChunks != 0 || fileHeader.Layout.DataChunks == 0 || fileHeader.Layout.DataChunks > 9 {
			return response, fmt.Errorf("%w: invalid mirror layout", errInvalidHeader)
		}
		layout = distribution.Layout{Type: distribution.LayoutMirror, NumChunks: uint8(fileHeader.Layout.DataChunks)}
	case layoutModeStripe:
		if fileHeader.Layout.ParityChunks != 1 || fileHeader.Layout.DataChunks == 0 || fileHeader.Layout.DataChunks > 8 {
			return response, fmt.Errorf("%w: invalid parity layout", errInvalidHeader)
		}
		layout = distribution.Layout{Type: distribution.LayoutParity, NumChunks: uint8(fileHeader.Layout.DataChunks)}
		stripeCapacity *= uint64(layout.NumChunks)
	default:
		return response, fmt.Errorf("%w: unknown layout mode", errInvalidHeader)
	}
	if stripeCapacity == 0 || stripeCapacity > uint64(int(^uint(0)>>1)) {
		return response, fmt.Errorf("%w: invalid stripe capacity", errInvalidHeader)
	}

	logicalStart := uint64(headerSize) + writeOffset
	logicalEnd := uint64(headerSize) + writeEnd
	oldLogicalEnd := uint64(headerSize) + fileHeader.Size
	firstStripe := logicalStart / stripeCapacity
	if writeOffset > fileHeader.Size {
		firstStripe = oldLogicalEnd / stripeCapacity
	}
	lastStripe := (logicalEnd - 1) / stripeCapacity
	headerLastStripe := (uint64(headerSize) - 1) / stripeCapacity

	staged := make(map[uint64]*stripeCacheEntry)
	load := func(stripeID uint64) error {
		if _, ok := staged[stripeID]; ok {
			return nil
		}
		stripeStart := stripeID * stripeCapacity
		entry, loadErr := layer.loadStripeEntryLocked(
			ctx, file, storage.StripeID(stripeID), stripeStart >= oldLogicalEnd,
		)
		if loadErr != nil {
			return loadErr
		}
		staged[stripeID] = entry
		return nil
	}

	for stripeID := uint64(0); stripeID <= headerLastStripe; stripeID++ {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		if err = load(stripeID); err != nil {
			return response, fmt.Errorf("load header stripe %d: %w", stripeID, err)
		}
	}
	for stripeID := firstStripe; stripeID <= lastStripe; stripeID++ {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		if err = load(stripeID); err != nil {
			return response, fmt.Errorf("load write stripe %d: %w", stripeID, err)
		}
	}

	if logicalStart > oldLogicalEnd {
		if err = extendStagedRange(staged, stripeCapacity, oldLogicalEnd, logicalStart); err != nil {
			return response, err
		}
	}
	if err = writeStagedRange(staged, stripeCapacity, logicalStart, request.Data); err != nil {
		return response, err
	}

	newHeader := fileHeader
	if writeEnd > newHeader.Size {
		newHeader.Size = writeEnd
	}
	now := time.Now()
	newHeader.Mtime = now
	newHeader.Ctime = now
	headerData, marshalErr := newHeader.MarshalBinary()
	if marshalErr != nil {
		return response, fmt.Errorf("encode updated header: %w", marshalErr)
	}
	if err = writeHeaderToStaged(staged, stripeCapacity, headerData); err != nil {
		return response, err
	}

	for stripeID, entry := range staged {
		entry.state = stripeCacheDirty
		file.stripeCache[stripeID] = entry
	}
	file.header = newHeader
	handle.activeStripeID = lastStripe
	handle.hasActiveStripeID = true
	response.Written = uint32(len(request.Data))
	return response, nil
}

func (layer *Orchestration) loadStripeEntryLocked(
	ctx context.Context, file *runtimeFile, stripeID storage.StripeID, allowMissing bool,
) (*stripeCacheEntry, error) {
	key := uint64(stripeID)
	if entry := file.stripeCache[key]; entry != nil {
		return &stripeCacheEntry{data: append([]byte(nil), entry.data...), state: entry.state}, nil
	}
	stripe, err := layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context: ctx, ObjectID: file.objectID, StripeID: stripeID,
		ChunkSize: file.header.Layout.ChunkSize,
	})
	if err != nil {
		if allowMissing && errors.Is(err, distribution.ErrStripeNotFound) {
			return &stripeCacheEntry{state: stripeCacheClean}, nil
		}
		return nil, err
	}
	return &stripeCacheEntry{data: append([]byte(nil), stripe.Data...), state: stripeCacheClean}, nil
}

func extendStagedRange(staged map[uint64]*stripeCacheEntry, capacity, start, end uint64) error {
	for position := start; position < end; {
		stripeID := position / capacity
		entry := staged[stripeID]
		if entry == nil {
			return fmt.Errorf("missing staged stripe %d: %w", stripeID, distribution.ErrDataCorrupted)
		}
		localStart := position % capacity
		count := capacity - localStart
		if remaining := end - position; count > remaining {
			count = remaining
		}
		localEnd := int(localStart + count)
		if len(entry.data) < localEnd {
			entry.data = append(entry.data, make([]byte, localEnd-len(entry.data))...)
		}
		position += count
	}
	return nil
}

func writeStagedRange(staged map[uint64]*stripeCacheEntry, capacity, start uint64, data []byte) error {
	for len(data) > 0 {
		stripeID := start / capacity
		entry := staged[stripeID]
		if entry == nil {
			return fmt.Errorf("missing staged stripe %d: %w", stripeID, distribution.ErrDataCorrupted)
		}
		localStart := int(start - stripeID*capacity)
		count := int(capacity) - localStart
		if count > len(data) {
			count = len(data)
		}
		localEnd := localStart + count
		if len(entry.data) < localEnd {
			entry.data = append(entry.data, make([]byte, localEnd-len(entry.data))...)
		}
		copy(entry.data[localStart:localEnd], data[:count])
		data = data[count:]
		start += uint64(count)
	}
	return nil
}

func writeHeaderToStaged(staged map[uint64]*stripeCacheEntry, capacity uint64, data []byte) error {
	return writeStagedRange(staged, capacity, 0, data)
}
