package distribution

import (
	"time"
)

func logOperation(layer *Distribution, name string, started time.Time, err error, attributes ...any) {
	duration := time.Since(started)
	if layer.trace != nil {
		layer.trace.Record("Distribution."+name, started, err)
	}
	layer.metricsCollector().recordOperation(name, duration, err)
	attributes = append(attributes, "duration", duration)
	if err != nil {
		attributes = append(attributes, "error", err)
	}
	layer.logger.Debug("Distribution operation completed", append(attributes, "operation", name)...)
}
