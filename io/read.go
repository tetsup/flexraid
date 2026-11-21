package io

import (
	"context"
	"os"
	"time"

	"github.com/tetsup/flexraid/storage"
)

// ReadRequest identifies the physical file to read.
type ReadRequest struct {
	Context context.Context
	Key     storage.FileKey
}

// ReadResponse contains the bytes read from the physical file.
type ReadResponse struct {
	Data []byte
}

// Read reads the complete physical file identified by the key.
func (layer *IO) Read(request ReadRequest) (response ReadResponse, err error) {
	started := operationStarted()
	var readErrors uint64
	defer func() {
		layer.recordOperation(OperationRead, started, err, readErrors)
		logResult(layer.logger, "Read", err,
			"key", request.Key, "duration", time.Since(started),
			"bytes", len(response.Data))
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
	response.Data, err = os.ReadFile(path)
	if err != nil {
		readErrors++
		layer.recordReadErrorFile(OperationRead, request.Key)
	}
	return response, err
}
