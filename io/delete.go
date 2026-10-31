package io

import (
	"context"
	"os"
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
	err = os.Remove(path)
	response.Deleted = err == nil
	return response, err
}
