package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
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

	initialObjects, err := readInitialObjects(ctx, request.Distribution)
	if err != nil {
		return NewResponse{}, err
	}

	layer := &Orchestration{
		distribution:  request.Distribution,
		logger:        logger,
		headers:       make(map[shared.ObjectID]header),
		deletionQueue: make(map[shared.ObjectID]struct{}),
	}
	if err := layer.initialize(initialObjects); err != nil {
		return NewResponse{}, err
	}
	logger.Debug("Orchestration initialized", "objects", len(layer.headers), "deletion_queue", len(layer.deletionQueue))
	return NewResponse{Layer: layer}, nil
}
