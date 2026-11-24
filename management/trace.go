package management

import (
	"context"
	"errors"
	"sync"
	"time"
)

const traceSpanLimit = 1_000_000

// TraceSpan records one completed Management operation using monotonic offsets.
type TraceSpan struct {
	ID         uint64        `json:"id"`
	Operation  string        `json:"operation"`
	StartNS    int64         `json:"start_ns"`
	DurationNS int64         `json:"duration_ns"`
	Error      string        `json:"error,omitempty"`
}

type TraceSnapshot struct {
	Enabled    bool        `json:"enabled"`
	Dropped    uint64      `json:"dropped"`
	Spans      []TraceSpan `json:"spans"`
}

type traceRecorder struct {
	mu      sync.Mutex
	enabled bool
	origin  time.Time
	nextID  uint64
	dropped uint64
	spans   []TraceSpan
}

func newTraceRecorder() *traceRecorder { return &traceRecorder{} }

func (r *traceRecorder) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = nil
	r.dropped = 0
	r.nextID = 0
	r.origin = time.Now()
	r.enabled = true
}

func (r *traceRecorder) stop() {
	r.mu.Lock()
	r.enabled = false
	r.mu.Unlock()
}

func (r *traceRecorder) record(operation string, started time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled { return }
	if len(r.spans) >= traceSpanLimit {
		r.dropped++
		return
	}
	r.nextID++
	span := TraceSpan{ID:r.nextID, Operation:operation, StartNS:started.Sub(r.origin).Nanoseconds(), DurationNS:time.Since(started).Nanoseconds()}
	if err != nil { span.Error = err.Error() }
	r.spans = append(r.spans, span)
}

func (r *traceRecorder) snapshot() TraceSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return TraceSnapshot{Enabled:r.enabled, Dropped:r.dropped, Spans:append([]TraceSpan(nil), r.spans...)}
}

type StartTraceRequest struct { Context context.Context }
type StartTraceResponse struct{}
type StopTraceRequest struct { Context context.Context }
type StopTraceResponse struct { Trace TraceSnapshot }
type GetTraceRequest struct { Context context.Context }
type GetTraceResponse struct { Trace TraceSnapshot }

func (layer *Management) StartTrace(request StartTraceRequest) (StartTraceResponse,error) {
	if request.Context == nil { return StartTraceResponse{}, errors.New("trace context is required") }
	if err:=request.Context.Err(); err!=nil { return StartTraceResponse{},err }
	layer.trace.start()
	return StartTraceResponse{},nil
}
func (layer *Management) StopTrace(request StopTraceRequest) (StopTraceResponse,error) {
	if request.Context == nil { return StopTraceResponse{}, errors.New("trace context is required") }
	if err:=request.Context.Err(); err!=nil { return StopTraceResponse{},err }
	layer.trace.stop()
	return StopTraceResponse{Trace:layer.trace.snapshot()},nil
}
func (layer *Management) GetTrace(request GetTraceRequest) (GetTraceResponse,error) {
	if request.Context == nil { return GetTraceResponse{}, errors.New("trace context is required") }
	if err:=request.Context.Err(); err!=nil { return GetTraceResponse{},err }
	return GetTraceResponse{Trace:layer.trace.snapshot()},nil
}
