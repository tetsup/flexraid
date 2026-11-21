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
	Context            context.Context
	Distribution       *distribution.Distribution
	LogLevel           slog.Level
	Logger             *slog.Logger
	CleanupPermissions *shared.CleanupPermissionStore
}
type NewResponse struct {
	Layer  *Orchestration
	RootID shared.ObjectID
}

// SetCleanupPermissionStore attaches the Management-owned shared permission state.
func (layer *Orchestration) SetCleanupPermissionStore(store *shared.CleanupPermissionStore) (err error) {
	started := time.Now()
	defer func() { layer.recordOperation("SetCleanupPermissionStore", started, err) }()
	if store == nil {
		return fmt.Errorf("cleanup permission store is required")
	}
	if err := layer.distribution.SetCleanupPermissionStore(store); err != nil {
		return err
	}
	layer.cleanupPermissionStore = store
	return nil
}

type Orchestration struct {
	distribution           *distribution.Distribution
	cleanupPermissionStore *shared.CleanupPermissionStore
	logger                 *slog.Logger
	headers                map[shared.ObjectID]header
	objects                map[shared.ObjectID]*runtimeObject
	root                   *runtimeObject
	deletionQueue          map[shared.ObjectID]struct{}
	protectedObjects       map[shared.ObjectID]struct{}
	cleanupWorkerMu        sync.Mutex
	cleanupWorkerCancel    context.CancelFunc
	cleanupWorkerDone      chan struct{}
	warnings               []initializationWarning
	metrics                *metricsCollector
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
		distribution:           request.Distribution,
		metrics:                newMetricsCollector(),
		cleanupPermissionStore: request.CleanupPermissions,
		logger:                 logger,
	}
	if request.CleanupPermissions != nil {
		if err := layer.SetCleanupPermissionStore(request.CleanupPermissions); err != nil {
			return NewResponse{}, err
		}
	}
	if err := layer.initialize(initialObjects); err != nil {
		return NewResponse{}, err
	}
	logger.Debug("Orchestration initialized", "root_id", layer.root.header.ObjectID.String(), "objects", len(layer.headers), "deletion_queue", len(layer.deletionQueue), "protected_objects", len(layer.protectedObjects), "warnings", len(layer.warnings))
	return NewResponse{Layer: layer, RootID: layer.root.header.ObjectID}, nil
}
