package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
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
	layer.mu.Lock()
	defer layer.mu.Unlock()
	return layer.readLocked(request)
}

func (layer *Orchestration) readLocked(request ReadRequest) (response ReadResponse, err error) {
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
	if handle.closed || handle.runtimeFile == nil {
		return response, os.ErrInvalid
	}
	if handle.accessMode == AccessModeWriteOnly {
		return response, os.ErrPermission
	}

	object, exists := layer.objects[request.ObjectID]
	if !exists {
		return response, os.ErrNotExist
	}
	if object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, fmt.Errorf("object %s is not a readable regular file: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if object.file == nil || object.file != handle.runtimeFile {
		return response, fmt.Errorf("handle does not refer to object %s: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	file := handle.runtimeFile
	fileHeader := file.header
	fileSize := fileHeader.Size
	if request.Size == 0 || request.Offset >= fileSize {
		return response, nil
	}
	remaining := fileSize - request.Offset
	readSize := uint64(request.Size)
	if readSize > remaining {
		readSize = remaining
	}
	response.Data = make([]byte, 0, int(readSize))
	position := request.Offset
	for uint64(len(response.Data)) < readSize {
		if err = ctx.Err(); err != nil {
			return ReadResponse{}, err
		}
		geometry, stripeOffset, locateErr := locateFileOffset(fileHeader, position)
		if locateErr != nil {
			return ReadResponse{}, locateErr
		}
		stripeData, readErr := layer.getStripeData(ctx, file, geometry)
		if readErr != nil {
			return ReadResponse{}, fmt.Errorf("read object %s stripe %d: %w", file.objectID.String(), geometry.StripeID, readErr)
		}
		if stripeOffset >= uint64(len(stripeData)) {
			return ReadResponse{}, fmt.Errorf("stripe %d does not contain requested offset: %w", geometry.StripeID, distribution.ErrDataCorrupted)
		}
		available := geometry.Capacity - (stripeOffset - geometry.DataOffset)
		if actual := uint64(len(stripeData)) - stripeOffset; available > actual {
			available = actual
		}
		count := readSize - uint64(len(response.Data))
		if count > available {
			count = available
		}
		response.Data = append(response.Data, stripeData[int(stripeOffset):int(stripeOffset+count)]...)
		position += count
	}
	return response, nil
}

func (layer *Orchestration) getStripeData(ctx context.Context, file *runtimeFile, geometry stripeGeometry) ([]byte, error) {
	stripeID := geometry.StripeID
	key := uint64(stripeID)
	entry := file.stripeCache[key]
	if entry != nil {
		data := append([]byte(nil), entry.data...)
		return data, nil
	}
	if file.stripeCache == nil {
		file.stripeCache = make(map[uint64]*stripeCacheEntry)
	}
	if entry = file.stripeCache[key]; entry != nil {
		return append([]byte(nil), entry.data...), nil
	}

	stripe, err := layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context:   ctx,
		ObjectID:  file.objectID,
		StripeID:  stripeID,
		ChunkSize: geometry.ChunkSize,
	})
	if err != nil {
		return nil, err
	}
	entry = &stripeCacheEntry{data: append([]byte(nil), stripe.Data...), state: stripeCacheClean}
	file.stripeCache[key] = entry
	return append([]byte(nil), entry.data...), nil
}
