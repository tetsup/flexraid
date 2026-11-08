package io

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/tetsup/flexraid/storage"
)

// SyncRequest identifies a physical file whose contents must reach stable storage.
type SyncRequest struct {
	Context context.Context
	Key     storage.FileKey
}

// SyncResponse reports successful synchronization of the physical file.
type SyncResponse struct{ Synced bool }

// Sync flushes the physical file identified by the key to stable storage.
func (layer *IO) Sync(request SyncRequest) (response SyncResponse, err error) {
	started := operationStarted()
	defer func() {
		logResult(layer.logger, "Sync", err,
			"key", request.Key, "duration", time.Since(started),
			"synced", response.Synced)
	}()

	if request.Context == nil {
		request.Context = context.Background()
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	path, pathErr := layer.filePath(request.Key)
	if pathErr != nil {
		err = pathErr
		return response, err
	}
	file, openErr := os.OpenFile(path, os.O_WRONLY, 0)
	if openErr != nil {
		err = openErr
		return response, err
	}
	if err = request.Context.Err(); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return response, err
	}
	if closeErr != nil {
		err = closeErr
		return response, err
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return response, err
	}
	response.Synced = true
	return response, nil
}


// syncDirectory persists the directory entry for a physical chunk file.
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
