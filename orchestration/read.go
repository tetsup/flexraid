package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type ReadRequest struct {
	Context  context.Context
	ObjectID shared.ObjectID
	Offset   uint64
	Size     uint32
}

type ReadResponse struct {
	Data []byte
}

// Read returns a byte range from a regular file or hard link.
// Header bytes are hidden from callers; Offset is relative to file content.
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
	object, exists := layer.objects[request.ObjectID]
	if !exists || object.header.Deleted {
		return response, os.ErrNotExist
	}
	if object.header.Kind != objectKindFile && object.header.Kind != objectKindHardLink {
		return response, fmt.Errorf("object %s is not a readable regular file: %w", request.ObjectID.String(), os.ErrInvalid)
	}
	if object.file == nil {
		return response, fmt.Errorf("file backing is unavailable: %w", os.ErrNotExist)
	}
	file := object.file
	if request.Size == 0 || request.Offset >= file.header.Size {
		return response, nil
	}
	remaining := file.header.Size - request.Offset
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
		stripe, readErr := layer.distribution.ReadStripe(distribution.ReadStripeRequest{
			Context: ctx, ObjectID: file.objectID, StripeID: stripeID,
			ChunkSize: file.header.Layout.ChunkSize,
		})
		if readErr != nil {
			return ReadResponse{}, fmt.Errorf("read object %s stripe %d: %w", file.objectID.String(), stripeID, readErr)
		}
		if stripeOffset >= uint64(len(stripe.Data)) {
			return ReadResponse{}, fmt.Errorf("stripe %d does not contain requested offset: %w", stripeID, distribution.ErrDataCorrupted)
		}
		available := uint64(len(stripe.Data)) - stripeOffset
		count := readSize - uint64(len(response.Data))
		if count > available {
			count = available
		}
		response.Data = append(response.Data, stripe.Data[int(stripeOffset):int(stripeOffset+count)]...)
		position += count
	}
	return response, nil
}

var _ = slog.LevelDebug
