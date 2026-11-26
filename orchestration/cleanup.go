package orchestration

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
)

type CleanupRequest struct {
	Context context.Context
}

type CleanupResponse struct {
	DeletedObjects int
	DeletedFiles   int
	SkippedObjects int
}

func (layer *Orchestration) CleanupLocked(request CleanupRequest) (response CleanupResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Cleanup", started, err) }()
	return layer.cleanupLocked(request)
}

func (layer *Orchestration) cleanupLocked(request CleanupRequest) (response CleanupResponse, err error) {
	started := time.Now()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() { logCleanup(layer.logger, started, err, response) }()

	permissions := shared.CleanupPermissionSnapshot{}
	if layer.cleanupPermissionStore != nil {
		permissions = layer.cleanupPermissionStore.Snapshot()
	}
	ids := make([]shared.ObjectID, 0, len(layer.deletionQueue))
	for objectID := range layer.deletionQueue {
		ids = append(ids, objectID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	if err = addObjectLocks(ctx, ids...); err != nil {
		return response, err
	}

	// Resolve the complete set of Objects whose physical files are all on
	// currently permitted drives. Distribution revalidates permission at each
	// Object boundary before beginning its ObjectID work unit.
	deletable := make([]shared.ObjectID, 0, len(ids))
	for _, objectID := range ids {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		object := layer.objects[objectID]
		if object != nil && object.file != nil && openHandleCount(object.file) > 0 {
			response.SkippedObjects++
			continue
		}
		drives, driveErr := layer.distribution.ObjectDriveIndexes(distribution.ObjectDriveIndexesRequest{
			Context: ctx, ObjectID: objectID,
		})
		if driveErr != nil {
			return response, fmt.Errorf("inspect drives for deleted Object %s: %w", objectID.String(), driveErr)
		}
		allowed := true
		if layer.cleanupPermissionStore != nil {
			for _, index := range drives.IOIndexes {
				if index < 0 || index >= len(permissions.Drives) || !permissions.Drives[index].DeleteLogicalObjectsAllowed {
					allowed = false
					break
				}
			}
		}
		if !allowed {
			response.SkippedObjects++
			continue
		}
		deletable = append(deletable, objectID)
	}

	result, deleteErr := layer.distribution.DeleteObjects(distribution.DeleteObjectsRequest{
		Context: ctx, ObjectIDs: deletable,
	})
	response.DeletedFiles += result.DeletedFiles
	response.SkippedObjects += result.SkippedObjects
	for _, objectID := range result.DeletedObjectIDs {
		delete(layer.deletionQueue, objectID)
		delete(layer.headers, objectID)
		delete(layer.objects, objectID)
		response.DeletedObjects++
	}
	return response, deleteErr
}

func logCleanup(logger interface{ Debug(string, ...any) }, started time.Time, err error, response CleanupResponse) {
	attrs := []any{"duration", time.Since(started), "deleted_objects", response.DeletedObjects,
		"deleted_files", response.DeletedFiles, "skipped_objects", response.SkippedObjects}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	logger.Debug("Orchestration cleanup completed", attrs...)
}
