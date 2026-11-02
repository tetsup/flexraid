package distribution

import (
	"log/slog"
	"time"
)

func logOperation(logger *slog.Logger, name string, started time.Time, err error, attributes ...any) {
	attributes = append(attributes, "duration", time.Since(started))
	if err != nil {
		attributes = append(attributes, "error", err)
	}
	logger.Debug("Distribution operation completed", append(attributes, "operation", name)...)
}
