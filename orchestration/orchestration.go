package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

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
	distribution     *distribution.Distribution
	logger           *slog.Logger
	headers          map[shared.ObjectID]header
	objects          map[shared.ObjectID]*runtimeObject
	root             *runtimeObject
	deletionQueue    map[shared.ObjectID]struct{}
	protectedObjects map[shared.ObjectID]struct{}
	warnings         []initializationWarning

	handlesMu sync.Mutex
	handles   map[*FileHandle]struct{}
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
		distribution: request.Distribution,
		logger:       logger,
		handles:      make(map[*FileHandle]struct{}),
	}
	if err := layer.initialize(initialObjects); err != nil {
		return NewResponse{}, err
	}
	logger.Debug("Orchestration initialized", "objects", len(layer.headers), "deletion_queue", len(layer.deletionQueue), "protected_objects", len(layer.protectedObjects), "warnings", len(layer.warnings))
	return NewResponse{Layer: layer}, nil
}
