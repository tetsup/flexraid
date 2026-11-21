package distribution

import (
	"context"
	"errors"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

// DeleteObjectsRequest submits one batch of logically deleted ObjectIDs.
type DeleteObjectsRequest struct {
	Context   context.Context
	ObjectIDs []shared.ObjectID
}

// DeleteObjectsResponse lists Objects whose physical data was fully removed.
type DeleteObjectsResponse struct {
	DeletedObjectIDs []shared.ObjectID
	DeletedFiles     int
	SkippedObjects   int
}

// DeleteObjects processes ObjectIDs in the supplied order. Permission is
// re-evaluated at each Object boundary; once DeleteObject starts, its captured
// permission snapshot governs that Object until completion.
func (d *Distribution) DeleteObjects(request DeleteObjectsRequest) (response DeleteObjectsResponse, err error) {
	started := time.Now()
	defer func() {
		logOperation(d, "DeleteObjects", started, err, "objects", len(request.ObjectIDs), "deleted_files", response.DeletedFiles)
	}()
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	for _, objectID := range request.ObjectIDs {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		result, deleteErr := d.DeleteObject(DeleteObjectRequest{Context: ctx, ObjectID: objectID})
		if errors.Is(deleteErr, ErrLogicalObjectDeletionNotAllowed) {
			response.SkippedObjects++
			continue
		}
		response.DeletedFiles += result.DeletedFiles
		if deleteErr != nil {
			return response, deleteErr
		}
		response.DeletedObjectIDs = append(response.DeletedObjectIDs, objectID)
	}
	return response, nil
}
