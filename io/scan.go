package io

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/tetsup/flexraid/storage"
)

// ScanRequest supplies the context and callback for a scan.
type ScanRequest struct {
	Context  context.Context
	Callback func(ScanEntry) error
}

// ScanResponse reports how many entries were passed to the callback.
type ScanResponse struct {
	Entries int
}

// Scan visits all physical files whose StripeID is zero.
// It does not choose a current StripeVersion or interpret file contents.
// A callback error stops the scan and is returned to the caller.
func (layer *IO) Scan(request ScanRequest) (response ScanResponse, err error) {
	started := operationStarted()
	defer func() {
		logResult(layer.logger, "Scan", err,
			"duration", time.Since(started), "entries", response.Entries)
	}()

	if request.Callback == nil {
		err = errors.New("scan callback is nil")
		return response, err
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}

	err = filepath.WalkDir(layer.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if contextErr := request.Context.Err(); contextErr != nil {
			return contextErr
		}
		if entry.IsDir() {
			return nil
		}

		relative, relErr := filepath.Rel(layer.root, path)
		if relErr != nil {
			return relErr
		}
		key, parseErr := storage.ParseFileKey(filepath.ToSlash(relative))
		if parseErr != nil {
			layer.logger.Warn("skipping file with invalid name",
				"path", relative, "error", parseErr)
			return nil
		}
		if key.StripeID != 0 {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if callbackErr := request.Callback(ScanEntry{Key: key, Data: data}); callbackErr != nil {
			return callbackErr
		}
		response.Entries++
		return nil
	})
	return response, err
}
