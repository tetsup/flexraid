package orchestration

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
)

type CleanupRequest struct {
	Context          context.Context
	OldVersions      bool
	InvalidStripes   bool
	IncompleteChunks bool
}
type CleanupResponse struct {
	DeletedObjects int
	DeletedFiles   int
}

func (layer *Orchestration) Cleanup(request CleanupRequest) (response CleanupResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() { logCleanup(layer.logger, started, err, response) }()

	ids := make([]shared.ObjectID, 0, len(layer.deletionQueue))
	for objectID := range layer.deletionQueue {
		ids = append(ids, objectID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, objectID := range ids {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		result, deleteErr := layer.distribution.DeleteObject(distribution.DeleteObjectRequest{Context: ctx, ObjectID: objectID})
		response.DeletedFiles += result.DeletedFiles
		if deleteErr != nil {
			err = errors.Join(err, deleteErr)
			continue
		}
		delete(layer.deletionQueue, objectID)
		delete(layer.headers, objectID)
		delete(layer.objects, objectID)
		response.DeletedObjects++
	}

	protected := make([]shared.ObjectID, 0, len(layer.protectedObjects))
	for id := range layer.protectedObjects {
		protected = append(protected, id)
	}
	result, cleanupErr := layer.distribution.Cleanup(distribution.CleanupRequest{
		Context: ctx, OldVersions: request.OldVersions, InvalidStripes: request.InvalidStripes,
		IncompleteChunks: request.IncompleteChunks, ExcludeObjectIDs: protected,
	})
	response.DeletedFiles += result.DeletedFiles
	return response, errors.Join(err, cleanupErr)
}

func logCleanup(logger interface{ Debug(string, ...any) }, started time.Time, err error, response CleanupResponse) {
	attrs := []any{"duration", time.Since(started), "deleted_objects", response.DeletedObjects, "deleted_files", response.DeletedFiles}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	logger.Debug("Orchestration cleanup completed", attrs...)
}
