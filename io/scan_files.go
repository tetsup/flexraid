package io

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

func (layer *IO) walkFiles(
	ctx context.Context,
	include func(storage.FileKey) bool,
	callback func(ScanEntry) error,
	onReadError func(storage.FileKey),
) (int, error) {
	count := 0
	err := filepath.WalkDir(layer.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			relative, err := filepath.Rel(layer.root, path)
			if err != nil {
				return err
			}
			first := strings.Split(filepath.ToSlash(relative), "/")[0]
			if first == config.PreservationDirectory {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(layer.root, path)
		if err != nil {
			return err
		}
		key, err := storage.ParseFileKey(filepath.ToSlash(relative))
		if err != nil {
			layer.logger.Warn("skipping file with invalid name", "path", relative, "error", err)
			return nil
		}
		if !include(key) {
			return nil
		}
		count++
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		sizeBytes := uint64(info.Size())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if onReadError != nil {
				onReadError(key)
			}
			if callbackErr := callback(ScanEntry{Key: key, SizeBytes: sizeBytes, Error: readErr}); callbackErr != nil {
				return callbackErr
			}
			return nil
		}
		if err := callback(ScanEntry{Key: key, Data: data, SizeBytes: sizeBytes}); err != nil {
			return err
		}
		return nil
	})
	return count, err
}
