package io

import (
	"github.com/tetsup/flexraid/storage"
	"log/slog"
	"os"
)

type IO struct {
	root   string
	logger *slog.Logger
}
type ScanEntry struct {
	Key   storage.FileKey
	Data  []byte
	Error error
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}
