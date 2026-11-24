package io

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tetsup/flexraid/storage"
)

const recentOperationLimit = 1000

// Operation identifies one public physical I/O operation.
type Operation string

const (
	OperationRead        Operation = "Read"
	OperationWrite       Operation = "Write"
	OperationDelete      Operation = "Delete"
	OperationListChunks  Operation = "ListChunks"
	OperationScanHeaders Operation = "ScanHeaders"
	OperationPreserve    Operation = "Preserve"
	OperationSync        Operation = "Sync"
)

var metricOperations = [...]Operation{
	OperationRead,
	OperationWrite,
	OperationDelete,
	OperationListChunks,
	OperationScanHeaders,
	OperationPreserve,
	OperationSync,
}

// RecentOperationMetrics summarizes the most recent operations for one command.
type RecentOperationMetrics struct {
	ProcessedCount uint64
	SuccessCount   uint64
	FailureCount   uint64
	ReadErrorCount uint64
	TotalDuration  time.Duration
	MaxDuration    time.Duration
}

// OperationMetrics contains cumulative and recent counters for one operation.
type OperationMetrics struct {
	SuccessCount             uint64
	FailureCount             uint64
	ReadErrorCount           uint64
	UniqueReadErrorFileCount uint64
	TotalDuration            time.Duration
	Recent                   RecentOperationMetrics
}

// GetMetricsRequest requests a consistent in-memory metrics snapshot.
type GetMetricsRequest struct {
	Context context.Context
}

// GetMetricsResponse contains an independent snapshot for each I/O operation.
type GetMetricsResponse struct {
	Operations map[Operation]OperationMetrics
}

type operationSample struct {
	success        bool
	readErrorCount uint64
	duration       time.Duration
}

type operationMetricState struct {
	successCount   uint64
	failureCount   uint64
	readErrorCount uint64
	totalDuration  time.Duration
	readErrorFiles map[storage.FileKey]struct{}
	samples        [recentOperationLimit]operationSample
	nextSample     int
	sampleCount    int
}

type metricsCollector struct {
	mu         sync.RWMutex
	operations map[Operation]*operationMetricState
}

func newMetricsCollector() *metricsCollector {
	collector := &metricsCollector{operations: make(map[Operation]*operationMetricState, len(metricOperations))}
	for _, operation := range metricOperations {
		collector.operations[operation] = &operationMetricState{
			readErrorFiles: make(map[storage.FileKey]struct{}),
		}
	}
	return collector
}

func (layer *IO) recordOperation(operation Operation, started time.Time, err error, readErrors uint64) {
	if layer.trace != nil {
		layer.trace.Record("IO."+string(operation), started, err)
	}
	if layer.metrics == nil {
		return
	}
	layer.metrics.record(operation, time.Since(started), err, readErrors)
}

func (layer *IO) recordReadErrorFile(operation Operation, key storage.FileKey) {
	if layer.metrics == nil {
		return
	}
	layer.metrics.recordReadErrorFile(operation, key)
}

func (collector *metricsCollector) recordReadErrorFile(operation Operation, key storage.FileKey) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	state := collector.operations[operation]
	if state == nil {
		state = &operationMetricState{readErrorFiles: make(map[storage.FileKey]struct{})}
		collector.operations[operation] = state
	}
	state.readErrorFiles[key] = struct{}{}
}

func (collector *metricsCollector) record(operation Operation, duration time.Duration, err error, readErrors uint64) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	state := collector.operations[operation]
	if state == nil {
		state = &operationMetricState{readErrorFiles: make(map[storage.FileKey]struct{})}
		collector.operations[operation] = state
	}
	if err != nil {
		state.failureCount++
	} else {
		state.successCount++
	}
	state.readErrorCount += readErrors
	state.totalDuration += duration

	state.samples[state.nextSample] = operationSample{
		success:        err == nil,
		readErrorCount: readErrors,
		duration:       duration,
	}
	state.nextSample = (state.nextSample + 1) % recentOperationLimit
	if state.sampleCount < recentOperationLimit {
		state.sampleCount++
	}
}

func (collector *metricsCollector) snapshot() map[Operation]OperationMetrics {
	collector.mu.RLock()
	defer collector.mu.RUnlock()

	result := make(map[Operation]OperationMetrics, len(collector.operations))
	for operation, state := range collector.operations {
		metrics := OperationMetrics{
			SuccessCount:             state.successCount,
			FailureCount:             state.failureCount,
			ReadErrorCount:           state.readErrorCount,
			UniqueReadErrorFileCount: uint64(len(state.readErrorFiles)),
			TotalDuration:            state.totalDuration,
		}
		for i := 0; i < state.sampleCount; i++ {
			index := (state.nextSample - state.sampleCount + i + recentOperationLimit) % recentOperationLimit
			sample := state.samples[index]
			metrics.Recent.ProcessedCount++
			if sample.success {
				metrics.Recent.SuccessCount++
			} else {
				metrics.Recent.FailureCount++
			}
			metrics.Recent.ReadErrorCount += sample.readErrorCount
			metrics.Recent.TotalDuration += sample.duration
			if sample.duration > metrics.Recent.MaxDuration {
				metrics.Recent.MaxDuration = sample.duration
			}
		}
		result[operation] = metrics
	}
	return result
}

// GetMetrics returns a consistent copy of the in-memory counters. The metrics
// query itself is excluded so that observing counters does not change them.
func (layer *IO) GetMetrics(request GetMetricsRequest) (response GetMetricsResponse, err error) {
	started := operationStarted()
	defer func() {
		logResult(layer.logger, "GetMetrics", err, "duration", time.Since(started))
	}()

	if request.Context == nil {
		return response, errors.New("metrics context is required")
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	response.Operations = layer.metrics.snapshot()
	return response, nil
}
