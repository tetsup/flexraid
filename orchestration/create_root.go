package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
)

// CreateRootRequest contains the storage and initial POSIX attributes for a
// newly created, empty FlexRAID filesystem.
type CreateRootRequest struct {
	Context      context.Context
	Distribution *distribution.Distribution
	Mode         uint32
	UID          uint32
	GID          uint32
	Logger       *slog.Logger
}

// CreateRootResponse contains the initialized filesystem and its root ObjectID.
type CreateRootResponse struct {
	Layer  *Orchestration
	RootID shared.ObjectID
}

// CreateRoot initializes an empty storage set by persisting its root Directory.
// It refuses to initialize storage that already contains any logical objects.
func CreateRoot(request CreateRootRequest) (response CreateRootResponse, err error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	if request.Distribution == nil {
		return response, fmt.Errorf("distribution is nil")
	}
	objects, err := request.Distribution.ListObjects(distribution.ListObjectsRequest{Context: ctx})
	if err != nil {
		return response, fmt.Errorf("check storage before root creation: %w", err)
	}
	if len(objects.ObjectIDs) != 0 {
		return response, fmt.Errorf("cannot create root: storage is not empty")
	}
	objectID, err := shared.NewObjectID()
	if err != nil {
		return response, fmt.Errorf("generate root ObjectID: %w", err)
	}
	now := time.Now()
	rootHeader := header{
		FormatVersion: headerFormatVersion,
		ObjectID:      objectID,
		Kind:          objectKindDirectory,
		Mode:          request.Mode & 07777,
		UID:           request.UID,
		GID:           request.GID,
		Atime:         now,
		Mtime:         now,
		Ctime:         now,
	}
	layer := &Orchestration{
		distribution: request.Distribution,
		logger:       request.Logger,
	}
	if layer.logger == nil {
		layer.logger = slog.Default()
	}
	rootHeader, err = layer.persistNewObject(ctx, rootHeader, nil)
	if err != nil {
		return response, fmt.Errorf("persist root directory: %w", err)
	}
	root := &runtimeObject{header: rootHeader, children: make(map[string]*runtimeObject)}
	layer.headers = map[shared.ObjectID]header{objectID: rootHeader}
	layer.objects = map[shared.ObjectID]*runtimeObject{objectID: root}
	layer.root = root
	layer.deletionQueue = make(map[shared.ObjectID]struct{})
	layer.protectedObjects = make(map[shared.ObjectID]struct{})
	return CreateRootResponse{Layer: layer, RootID: objectID}, nil
}
