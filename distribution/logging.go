package distribution

import (
	"log/slog"
	"time"
)

func logOperation(layer *Distribution, name string, started time.Time, err error, attributes ...any) {
	duration := time.Since(started)
	layer.metrics.recordOperation(name, duration, err)
	attributes = append(attributes, "duration", duration)
	if err != nil { attributes = append(attributes, "error", err) }
	layer.logger.Debug("Distribution operation completed", append(attributes, "operation", name)...)
}
