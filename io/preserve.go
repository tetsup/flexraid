package io

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

// PreserveRequest identifies a physical chunk to move into the drive-local
// preservation directory. The original physical filename is retained.
type PreserveRequest struct {
	Context context.Context
	Key     storage.FileKey
}

// PreserveResponse reports whether the source file was moved.
type PreserveResponse struct {
	Moved bool
}

// Preserve atomically renames a physical chunk into the fixed preservation
// directory under this I/O root. It never replaces an existing destination.
func (layer *IO) Preserve(request PreserveRequest) (response PreserveResponse, err error) {
	started := operationStarted()
	defer func() {
		layer.recordOperation(OperationPreserve, started, err, 0)
		logResult(layer.logger, "Preserve", err,
			"key", request.Key, "duration", time.Since(started), "moved", response.Moved)
	}()

	if request.Context == nil {
		return response, errors.New("preserve context is required")
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	source, err := layer.filePath(request.Key)
	if err != nil {
		return response, err
	}
	relative, err := request.Key.Path()
	if err != nil {
		return response, err
	}
	layer.recordAccess()
	defer layer.recordAccess()
	rootInfo, statErr := os.Stat(layer.preservationRoot)
	if statErr != nil {
		return response, fmt.Errorf("stat preservation root %q: %w", layer.preservationRoot, statErr)
	}
	if !rootInfo.IsDir() {
		return response, fmt.Errorf("preservation root %q is not a directory", layer.preservationRoot)
	}
	destinationDir := filepath.Join(layer.preservationRoot, config.PreservationDirectory)
	if err = os.MkdirAll(destinationDir, 0o700); err != nil {
		return response, err
	}
	destination := filepath.Join(destinationDir, filepath.Base(relative))

	// A previous attempt may have completed the rename before its caller
	// observed success. Treat that exact state as an idempotent completion.
	_, sourceErr := os.Lstat(source)
	_, destinationErr := os.Lstat(destination)
	if errors.Is(sourceErr, os.ErrNotExist) && destinationErr == nil {
		return PreserveResponse{Moved: true}, nil
	}
	if sourceErr != nil {
		return response, sourceErr
	}
	if destinationErr == nil {
		return response, os.ErrExist
	}
	if !errors.Is(destinationErr, os.ErrNotExist) {
		return response, destinationErr
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	if err = os.Rename(source, destination); err != nil {
		// Resolve the race where another attempt moved this same source.
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Lstat(destination); statErr == nil {
				return PreserveResponse{Moved: true}, nil
			}
		}
		return response, err
	}
	response.Moved = true
	return response, nil
}
