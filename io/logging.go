package io

import (
	"log/slog"
	"time"
)

// logResult records the outcome of a public operation at DEBUG level.
// Severity decisions for exceptional conditions remain with each operation.
func logResult(logger *slog.Logger, operation string, err error, attributes ...any) {
	attributes = append(attributes, "operation", operation)
	if err != nil {
		attributes = append(attributes, "result", "error", "error", err)
	} else {
		attributes = append(attributes, "result", "success")
	}
	logger.Debug("I/O operation completed", attributes...)
}

func operationStarted() time.Time {
	return time.Now()
}
