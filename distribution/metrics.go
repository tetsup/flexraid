package distribution

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

const recentOperationLimit = 1000

type OperationMetrics struct {
	SuccessCount  uint64
	FailureCount  uint64
	TotalDuration time.Duration
	Recent        RecentOperationMetrics
}
type RecentOperationMetrics struct {
	ProcessedCount uint64
	SuccessCount   uint64
	FailureCount   uint64
	TotalDuration  time.Duration
	MaxDuration    time.Duration
}
type StripeMetrics struct {
	ObjectCount               uint64
	StripeCount               uint64
	RecoverableStripeCount    uint64
	UnrecoverableStripeCount  uint64
	CorruptedStripeCount      uint64
	RecoverableVersionCount   uint64
	UnrecoverableVersionCount uint64
	IncompleteVersionCount    uint64
	MissingChunkCount         uint64
	OrphanChunkCount          uint64
}
type GetMetricsRequest struct{ Context context.Context }
type GetMetricsResponse struct {
	Operations           map[string]OperationMetrics
	Stripes              StripeMetrics
	IntegrityErrorCount  uint64
	RecoveryCount        uint64
	RecoveryFailureCount uint64
}
type operationSample struct {
	success  bool
	duration time.Duration
}
type operationMetricState struct {
	successCount  uint64
	failureCount  uint64
	totalDuration time.Duration
	samples       [recentOperationLimit]operationSample
	nextSample    int
	sampleCount   int
}
type metricsCollector struct {
	mu                   sync.RWMutex
	operations           map[string]*operationMetricState
	integrityErrorCount  uint64
	recoveryCount        uint64
	recoveryFailureCount uint64
	chunks map[string]*ChunkOperationMetrics
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{operations: make(map[string]*operationMetricState), chunks: make(map[string]*ChunkOperationMetrics)}
}
func (c *metricsCollector) recordOperation(name string, duration time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.operations[name]
	if state == nil {
		state = &operationMetricState{}
		c.operations[name] = state
	}
	if err != nil {
		state.failureCount++
	} else {
		state.successCount++
	}
	state.totalDuration += duration
	state.samples[state.nextSample] = operationSample{success: err == nil, duration: duration}
	state.nextSample = (state.nextSample + 1) % recentOperationLimit
	if state.sampleCount < recentOperationLimit {
		state.sampleCount++
	}
}
func (d *Distribution) recordChunkOperation(kind string, err error) {
	d.metrics.mu.Lock(); defer d.metrics.mu.Unlock()
	value := d.metrics.chunks[kind]; if value == nil { value = &ChunkOperationMetrics{}; d.metrics.chunks[kind] = value }
	if err != nil { value.FailureCount++ } else { value.SuccessCount++ }
}
func (d *Distribution) recordIntegrityError() {
	d.metrics.mu.Lock()
	d.metrics.integrityErrorCount++
	d.metrics.mu.Unlock()
}
func (d *Distribution) recordRecovery(success bool) {
	d.metrics.mu.Lock()
	if success {
		d.metrics.recoveryCount++
	} else {
		d.metrics.recoveryFailureCount++
	}
	d.metrics.mu.Unlock()
}
func (c *metricsCollector) snapshot() (map[string]OperationMetrics, ChunkMetrics, uint64, uint64, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	operations := make(map[string]OperationMetrics, len(c.operations))
	for name, state := range c.operations {
		value := OperationMetrics{SuccessCount: state.successCount, FailureCount: state.failureCount, TotalDuration: state.totalDuration}
		for i := 0; i < state.sampleCount; i++ {
			index := (state.nextSample - state.sampleCount + i + recentOperationLimit) % recentOperationLimit
			sample := state.samples[index]
			value.Recent.ProcessedCount++
			if sample.success {
				value.Recent.SuccessCount++
			} else {
				value.Recent.FailureCount++
			}
			value.Recent.TotalDuration += sample.duration
			if sample.duration > value.Recent.MaxDuration {
				value.Recent.MaxDuration = sample.duration
			}
		}
		operations[name] = value
	}
	chunks := ChunkMetrics{}
	for kind, value := range c.chunks { switch kind { case "Read": chunks.Read=*value; case "Write": chunks.Write=*value; case "Delete": chunks.Delete=*value; case "Preserve": chunks.Preserve=*value; case "Sync": chunks.Sync=*value } }
	return operations, chunks, c.integrityErrorCount, c.recoveryCount, c.recoveryFailureCount
}

// GetMetrics returns Distribution-owned statistics only; physical I/O metrics remain owned by I/O.
func (d *Distribution) GetMetrics(request GetMetricsRequest) (response GetMetricsResponse, err error) {
	started := time.Now()
	defer func() {
		d.logger.Debug("Distribution metrics snapshot completed", "duration", time.Since(started), "error", err)
	}()
	if request.Context == nil {
		return response, errors.New("metrics context is required")
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	response.Operations, response.Chunks, response.IntegrityErrorCount, response.RecoveryCount, response.RecoveryFailureCount = d.metrics.snapshot()
	d.mapMu.Lock()
	defer d.mapMu.Unlock()
	objects := make(map[shared.ObjectID]struct{})
	stripes := make(map[stripeIDKey]struct{})
	for objectID, entries := range d.objects {
		objects[objectID] = struct{}{}
		for stripeID := range entries {
			stripes[stripeIDKey{objectID: objectID, stripeID: stripeID}] = struct{}{}
		}
	}
	for key := range d.versions {
		objects[key.objectID] = struct{}{}
		stripes[key] = struct{}{}
	}
	for objectID, locations := range d.orphans {
		objects[objectID] = struct{}{}
		response.Stripes.OrphanChunkCount += uint64(len(locations))
		for _, location := range locations {
			stripes[stripeIDKey{objectID: objectID, stripeID: location.key.StripeID}] = struct{}{}
		}
	}
	response.Stripes.ObjectCount = uint64(len(objects))
	response.Stripes.StripeCount = uint64(len(stripes))
	for key := range stripes {
		versions := d.versions[key]
		for _, version := range versions {
			if len(version.chunks) < len(expectedChunkNos(version.method)) {
				response.Stripes.IncompleteVersionCount++
			}
			if version.recoverable {
				response.Stripes.RecoverableVersionCount++
			} else {
				response.Stripes.UnrecoverableVersionCount++
			}
		}
		current := d.objects[key.objectID][key.stripeID]
		if current == nil {
			response.Stripes.UnrecoverableStripeCount++
			continue
		}
		selected := versions[current.version]
		if selected != nil && selected.recoverable {
			response.Stripes.RecoverableStripeCount++
		} else {
			response.Stripes.UnrecoverableStripeCount++
		}
		if current.corrupted {
			response.Stripes.CorruptedStripeCount++
		}
		if selected != nil {
			expected := len(expectedChunkNos(selected.method))
			if expected > len(selected.chunks) {
				response.Stripes.MissingChunkCount += uint64(expected - len(selected.chunks))
			}
		}
	}
	return response, nil
}
