package io

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/tetsup/flexraid/storage"
)

// WriteRequest identifies the destination file and data to write.
type WriteRequest struct {
	Context context.Context
	Key     storage.FileKey
	Data    []byte
}

// WriteResponse reports the number of bytes written.
type WriteResponse struct {
	BytesWritten int
}

// Write creates a physical file without replacing an existing file.
func (layer *IO) Write(request WriteRequest) (response WriteResponse, err error) {
	started := operationStarted()
	defer func() {
		logResult(layer.logger, "Write", err,
			"key", request.Key, "duration", time.Since(started),
			"bytes", response.BytesWritten)
	}()

	if err = request.Context.Err(); err != nil {
		return response, err
	}
	path, pathErr := layer.filePath(request.Key)
	if pathErr != nil {
		err = pathErr
		return response, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return response, err
	}

	file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if openErr != nil {
		err = openErr
		return response, err
	}
	written, writeErr := file.Write(request.Data)
	response.BytesWritten = written
	if writeErr != nil {
		_ = file.Close()
		_ = os.Remove(path)
		err = writeErr
		return response, err
	}
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(path)
		err = closeErr
		return response, err
	}
	return response, nil
}
