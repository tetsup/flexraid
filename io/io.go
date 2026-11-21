package io

import (
	"log/slog"
	"os"

	"github.com/tetsup/flexraid/storage"
)

type IO struct {
	root             string
	preservationRoot string
	logger           *slog.Logger
	metrics          *metricsCollector
}

type ScanEntry struct {
	Key   storage.FileKey
	Data  []byte
	Error error
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
