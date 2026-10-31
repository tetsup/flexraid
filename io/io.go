// Package io implements physical file operations beneath a storage root.
package io

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/tetsup/flexraid/storage"
)

// IO operates on physical files beneath one root directory.
type IO struct {
	root string
}

// New creates an I/O layer rooted at root.
func New(root string) *IO {
	return &IO{root: root}
}

// ScanEntry contains a stripe-zero file discovered by Scan.
type ScanEntry struct {
	Key  storage.FileKey
	Data []byte
}

// Read reads the complete physical file identified by key.
func (layer *IO) Read(ctx context.Context, key storage.FileKey) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := layer.filePath(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Write creates a physical file without replacing an existing file.
func (layer *IO) Write(ctx context.Context, key storage.FileKey, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := layer.filePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Scan visits all physical files whose StripeID is zero.
// It does not choose a current StripeVersion or interpret file contents.
// Returning an error from callback stops the scan.
func (layer *IO) Scan(ctx context.Context, callback func(ScanEntry) error) error {
	if callback == nil {
		return errors.New("scan callback is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return filepath.WalkDir(layer.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		relative, err := filepath.Rel(layer.root, path)
		if err != nil {
			return err
		}
		key, err := storage.ParseFileKey(filepath.ToSlash(relative))
		if err != nil || key.StripeID != 0 {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return callback(ScanEntry{Key: key, Data: data})
	})
}

// Delete removes the physical file identified by key.
func (layer *IO) Delete(ctx context.Context, key storage.FileKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := layer.filePath(key)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (layer *IO) filePath(key storage.FileKey) (string, error) {
	relative, err := key.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(layer.root, filepath.FromSlash(relative)), nil
}
