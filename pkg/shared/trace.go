package shared

import "time"

// TraceRecorder accepts completed operation spans from the filesystem layers.
type TraceRecorder interface {
	Record(operation string, started time.Time, err error)
}
