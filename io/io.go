package io

import (
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/tetsup/flexraid/storage"
)

type IO struct {
	root             string
	preservationRoot string
	logger           *slog.Logger
	metrics          *metricsCollector
	lastAccess       atomic.Int64
}

type ScanEntry struct {
	Key       storage.FileKey
	Data      []byte
	SizeBytes uint64
	Error     error
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
