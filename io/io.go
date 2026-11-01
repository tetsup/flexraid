package io

import (
 "log/slog"
 "os"
 "github.com/tetsup/flexraid/storage"
)
type IO struct { root string; logger *slog.Logger }
type ScanEntry struct { Key storage.FileKey; Data []byte }
func newLogger(level slog.Level)*slog.Logger{return slog.New(slog.NewTextHandler(os.Stderr,&slog.HandlerOptions{Level:level}))}
