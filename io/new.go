package io

import "log/slog"

// NewRequest configures a new I/O layer.
type NewRequest struct {
	Root     string
	LogLevel slog.Level
	Logger   *slog.Logger
}

// NewResponse contains the initialized I/O layer.
type NewResponse struct {
	Layer *IO
}

// New creates an I/O layer rooted at the requested directory.
func New(request NewRequest) NewResponse {
	logger := request.Logger
	if logger == nil {
		logger = newLogger(request.LogLevel)
	}
	layer := &IO{root: request.Root, logger: logger}
	logResult(logger, "New", nil, "root", request.Root)
	return NewResponse{Layer: layer}
}
