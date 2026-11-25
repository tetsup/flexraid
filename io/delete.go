package io

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/tetsup/flexraid/storage"
)

// DeleteRequest identifies the physical file to remove.
type DeleteRequest struct {
	Context context.Context
	Key     storage.FileKey
}

// DeleteResponse reports whether the file was removed.
type DeleteResponse struct {
	Deleted bool
}

// Delete removes the physical file identified by the key.
func (layer *IO) Delete(request DeleteRequest) (response DeleteResponse, err error) {
	started := operationStarted()
	defer func() {
		layer.recordOperation(OperationDelete, started, err, 0)
		logResult(layer.logger, "Delete", err,
			"key", request.Key, "duration", time.Since(started),
			"deleted", response.Deleted)
	}()

	if err = request.Context.Err(); err != nil {
		return response, err
	}
	path, pathErr := layer.filePath(request.Key)
	if pathErr != nil {
		err = pathErr
		return response, err
	}
	layer.recordAccess()
	defer layer.recordAccess()
	removeErr := os.Remove(path)
	// Version reuse is safe only after the directory entry removal is durable.
	// Sync the parent even for an already absent file, which also completes a
	// previous attempt whose unlink succeeded but directory Sync failed.
	if syncErr := syncDirectory(filepath.Dir(path)); syncErr != nil {
		return response, syncErr
	}
	if removeErr != nil {
		return response, removeErr
	}
	response.Deleted = true
	return response, nil
}
