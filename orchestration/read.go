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

type ReadRequest struct {
	Context  context.Context
	Handle   *FileHandle
	ObjectID shared.ObjectID
	Offset   uint64
	Size     uint32
}

type ReadResponse struct {
	Data []byte
}

// Read returns a byte range from a regular file or hard link.
// Header bytes are hidden from callers; Offset is relative to file content.
// A valid handle from Open is mandatory.
func (layer *Orchestration) Read(request ReadRequest) (response ReadResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if layer.logger != nil {
			layer.logger.Debug("Orchestration Read completed",
				"object_id", request.ObjectID.String(),
				"offset", request.Offset,
				"requested_bytes", request.Size,
				"read_bytes", len(response.Data),
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
	handle := request.Handle
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed || handle.runtimeFile == nil {
		return response, os.ErrInvalid
	}
	if handle.accessMode == AccessModeWriteOnly {
		return response, os.ErrPermission
	}

	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return response, os.ErrNotExist
	}
	if object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, fmt.Errorf("object %s is not a readable regular file: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if object.file == nil || object.file != handle.runtimeFile {
		return response, fmt.Errorf("handle does not refer to object %s: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	file := handle.runtimeFile

	file.mu.RLock()
	fileSize := file.header.Size
	file.mu.RUnlock()
	if request.Size == 0 || request.Offset >= fileSize {
		return response, nil
	}
	remaining := fileSize - request.Offset
	readSize := uint64(request.Size)
	if readSize > remaining {
		readSize = remaining
	}
	if file.header.Layout.ChunkSize == 0 {
		return response, fmt.Errorf("%w: zero chunk size", errInvalidHeader)
	}

	var layout distribution.Layout
	switch file.header.Layout.Mode {
	case layoutModeMirror:
		if file.header.Layout.ParityChunks != 0 {
			return response, fmt.Errorf("%w: mirror layout has parity chunks", errInvalidHeader)
		}
		layout = distribution.Layout{Type: distribution.LayoutMirror, NumChunks: uint8(file.header.Layout.DataChunks)}
	case layoutModeStripe:
		if file.header.Layout.ParityChunks != 1 {
			return response, fmt.Errorf("%w: stripe layout requires one parity chunk", errInvalidHeader)
		}
		layout = distribution.Layout{Type: distribution.LayoutParity, NumChunks: uint8(file.header.Layout.DataChunks)}
	default:
		return response, fmt.Errorf("%w: unknown layout mode", errInvalidHeader)
	}
	if layout.NumChunks == 0 {
		return response, fmt.Errorf("%w: zero data chunks", errInvalidHeader)
	}

	stripeCapacity := uint64(file.header.Layout.ChunkSize)
	if layout.Type == distribution.LayoutParity {
		stripeCapacity *= uint64(layout.NumChunks)
	}
	if stripeCapacity == 0 {
		return response, fmt.Errorf("%w: invalid stripe capacity", errInvalidHeader)
	}

	response.Data = make([]byte, 0, int(readSize))
	position := request.Offset
	for uint64(len(response.Data)) < readSize {
		if err = ctx.Err(); err != nil {
			return ReadResponse{}, err
		}
		logicalOffset := uint64(headerSize) + position
		stripeID := storage.StripeID(logicalOffset / stripeCapacity)
		stripeOffset := logicalOffset % stripeCapacity

		stripeData, readErr := layer.getStripeData(ctx, file, stripeID)
		if readErr != nil {
			return ReadResponse{}, fmt.Errorf("read object %s stripe %d: %w", file.objectID.String(), stripeID, readErr)
		}
		if stripeOffset >= uint64(len(stripeData)) {
			return ReadResponse{}, fmt.Errorf("stripe %d does not contain requested offset: %w", stripeID, distribution.ErrDataCorrupted)
		}
		available := uint64(len(stripeData)) - stripeOffset
		count := readSize - uint64(len(response.Data))
		if count > available {
			count = available
		}
		response.Data = append(response.Data, stripeData[int(stripeOffset):int(stripeOffset+count)]...)
		position += count
	}
	return response, nil
}

func (layer *Orchestration) getStripeData(ctx context.Context, file *runtimeFile, stripeID storage.StripeID) ([]byte, error) {
	key := uint64(stripeID)

	file.mu.RLock()
	entry := file.stripeCache[key]
	if entry != nil {
		data := entry.data
		file.mu.RUnlock()
		return data, nil
	}
	file.mu.RUnlock()

	file.mu.Lock()
	defer file.mu.Unlock()
	if file.stripeCache == nil {
		file.stripeCache = make(map[uint64]*stripeCacheEntry)
	}
	if entry = file.stripeCache[key]; entry != nil {
		return entry.data, nil
	}

	stripe, err := layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context: ctx,
		ObjectID: file.objectID,
		StripeID: stripeID,
		ChunkSize: file.header.Layout.ChunkSize,
	})
	if err != nil {
		return nil, err
	}
	entry = &stripeCacheEntry{data: append([]byte(nil), stripe.Data...), state: stripeCacheClean}
	file.stripeCache[key] = entry
	return entry.data, nil
}
