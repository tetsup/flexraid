package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type NewRequest struct {
	Context      context.Context
	Distribution *distribution.Distribution
	LogLevel     slog.Level
	Logger       *slog.Logger
}
type NewResponse struct{ Layer *Orchestration }

type Orchestration struct {
	distribution  *distribution.Distribution
	logger        *slog.Logger
	headers       map[shared.ObjectID]header
	deletionQueue map[shared.ObjectID]struct{}
}

func New(request NewRequest) (NewResponse, error) {
	logger := request.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: request.LogLevel}))
	}
	if request.Distribution == nil {
		return NewResponse{}, fmt.Errorf("distribution is nil")
	}
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return NewResponse{}, err
	}
	layer := &Orchestration{distribution: request.Distribution, logger: logger, headers: make(map[shared.ObjectID]header), deletionQueue: make(map[shared.ObjectID]struct{})}
	objects, err := request.Distribution.ListObjects(distribution.ListObjectsRequest{Context: ctx})
	if err != nil {
		return NewResponse{}, err
	}
	for _, objectID := range objects.ObjectIDs {
		headerData := make([]byte, 0, headerSize)
		for stripeID := uint64(0); len(headerData) < headerSize; stripeID++ {
			if err := ctx.Err(); err != nil {
				return NewResponse{}, err
			}
			read, readErr := request.Distribution.ReadStripe(distribution.ReadStripeRequest{Context: ctx, ObjectID: objectID, StripeID: storage.StripeID(stripeID)})
			if readErr != nil {
				return NewResponse{}, fmt.Errorf("read object header %s at stripe %d: %w", objectID.String(), stripeID, readErr)
			}
			if len(read.Data) == 0 {
				return NewResponse{}, fmt.Errorf("%w: empty stripe while reading header for %s", errInvalidHeader, objectID.String())
			}
			headerData = append(headerData, read.Data...)
		}
		var objectHeader header
		if err := objectHeader.UnmarshalBinary(headerData[:headerSize]); err != nil {
			return NewResponse{}, fmt.Errorf("decode object header %s: %w", objectID.String(), err)
		}
		if objectHeader.ObjectID != objectID {
			return NewResponse{}, fmt.Errorf("%w: object ID mismatch", errInvalidHeader)
		}
		layer.headers[objectID] = objectHeader
		if objectHeader.Deleted {
			layer.deletionQueue[objectID] = struct{}{}
		}
	}
	logger.Debug("Orchestration initialized", "objects", len(layer.headers), "deletion_queue", len(layer.deletionQueue))
	return NewResponse{Layer: layer}, nil
}
