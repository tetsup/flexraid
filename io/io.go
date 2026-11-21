package io

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/tetsup/flexraid/storage"
)

type IO struct {
	root             string
	preservationRoot string
	logger           *slog.Logger
	metrics          *metricsCollector
	lastAccess       atomic.Int64
	device           *deviceState
	monitorMu        sync.Mutex
	monitorWake      chan struct{}
	monitorStop      chan struct{}
	monitorDone      chan struct{}
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
