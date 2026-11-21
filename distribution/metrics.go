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
type ChunkOperationMetrics struct {
	SuccessCount         uint64
	FailureCount         uint64
	RecentProcessedCount uint64
	RecentSuccessCount   uint64
	RecentFailureCount   uint64
}
type ChunkMetrics struct {
	Read     ChunkOperationMetrics
	Write    ChunkOperationMetrics
	Delete   ChunkOperationMetrics
	Preserve ChunkOperationMetrics
	Sync     ChunkOperationMetrics
}
type GetMetricsRequest struct{ Context context.Context }
type GetMetricsResponse struct {
	Operations           map[string]OperationMetrics
	Chunks               ChunkMetrics
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
	chunks               map[string]*chunkMetricState
}

type chunkMetricState struct {
	metrics ChunkOperationMetrics
	samples [recentOperationLimit]bool
	next    int
	count   int
}

func (d *Distribution) metricsCollector() *metricsCollector {
	d.metricsOnce.Do(func() {
		if d.metrics == nil {
			d.metrics = newMetricsCollector()
		}
	})
	return d.metrics
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{operations: make(map[string]*operationMetricState), chunks: make(map[string]*chunkMetricState)}
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
	d.metricsCollector().mu.Lock()
	defer d.metricsCollector().mu.Unlock()
	value := d.metricsCollector().chunks[kind]
	if value == nil {
		value = &chunkMetricState{}
		d.metrics.chunks[kind] = value
	}
	if err != nil {
		value.metrics.FailureCount++
	} else {
		value.metrics.SuccessCount++
	}
	value.samples[value.next] = err == nil
	value.next = (value.next + 1) % recentOperationLimit
	if value.count < recentOperationLimit {
		value.count++
	}
}
func (d *Distribution) recordIntegrityError() {
	d.metrics.mu.Lock()
	d.metricsCollector().integrityErrorCount++
	d.metrics.mu.Unlock()
}
func (d *Distribution) recordRecovery(success bool) {
	d.metrics.mu.Lock()
	if success {
		d.metricsCollector().recoveryCount++
	} else {
		d.metricsCollector().recoveryFailureCount++
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
	for kind, value := range c.chunks {
		metrics := value.metrics
		for i := 0; i < value.count; i++ {
			index := (value.next - value.count + i + recentOperationLimit) % recentOperationLimit
			metrics.RecentProcessedCount++
			if value.samples[index] {
				metrics.RecentSuccessCount++
			} else {
				metrics.RecentFailureCount++
			}
		}
		switch kind {
		case "Read":
			chunks.Read = metrics
		case "Write":
			chunks.Write = metrics
		case "Delete":
			chunks.Delete = metrics
		case "Preserve":
			chunks.Preserve = metrics
		case "Sync":
			chunks.Sync = metrics
		}
	}
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
	d.mapMu.Lock()
	defer d.mapMu.Unlock()
	response.Operations, response.Chunks, response.IntegrityErrorCount, response.RecoveryCount, response.RecoveryFailureCount = d.metricsCollector().snapshot()
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
			expectedChunks := len(expectedChunkNos(version.method))
			missingCount := expectedChunks - len(version.chunks) + len(version.missing)
			if missingCount > 0 {
				response.Stripes.IncompleteVersionCount++
				response.Stripes.MissingChunkCount += uint64(missingCount)
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
			for _, version := range versions {
				if version.invalid || !version.recoverable {
					response.Stripes.CorruptedStripeCount++
					break
				}
			}
			continue
		}
		selected := versions[current.version]
		if selected != nil && selected.recoverable {
			response.Stripes.RecoverableStripeCount++
		} else {
			response.Stripes.UnrecoverableStripeCount++
		}
		if current.corrupted || selected == nil || selected.invalid || !selected.recoverable {
			response.Stripes.CorruptedStripeCount++
		}
	}
	return response, nil
}
