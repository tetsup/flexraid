// Package io implements physical file operations beneath a storage root.
package io

import (
	"log/slog"
	"os"

	"github.com/tetsup/flexraid/storage"
)

// IO operates on physical files beneath one root directory.
type IO struct {
	root   string
	logger *slog.Logger
}

// ScanEntry contains a stripe-zero file discovered by Scan.
type ScanEntry struct {
	Key  storage.FileKey
	Data []byte
}

// newLogger creates a logger whose threshold is configured at runtime.
func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
