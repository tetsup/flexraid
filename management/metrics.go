package management

import (
	"context"
	"errors"
	"sync"
	"time"
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

type GetMetricsRequest struct {
	Context context.Context
}

type GetMetricsResponse struct {
	Operations map[string]OperationMetrics
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
	next          int
	count         int
}

type metricsCollector struct {
	mu         sync.RWMutex
	operations map[string]*operationMetricState
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{operations: make(map[string]*operationMetricState)}
}

func (layer *Management) recordOperation(name string, started time.Time, err error) {
	if layer.metrics == nil {
		return
	}
	layer.metrics.record(name, time.Since(started), err)
}

func (collector *metricsCollector) record(name string, duration time.Duration, err error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	state := collector.operations[name]
	if state == nil {
		state = &operationMetricState{}
		collector.operations[name] = state
	}
	if err == nil {
		state.successCount++
	} else {
		state.failureCount++
	}
	state.totalDuration += duration
	state.samples[state.next] = operationSample{success: err == nil, duration: duration}
	state.next = (state.next + 1) % recentOperationLimit
	if state.count < recentOperationLimit {
		state.count++
	}
}

func (collector *metricsCollector) snapshot() map[string]OperationMetrics {
	collector.mu.RLock()
	defer collector.mu.RUnlock()

	result := make(map[string]OperationMetrics, len(collector.operations))
	for name, state := range collector.operations {
		value := OperationMetrics{
			SuccessCount:  state.successCount,
			FailureCount:  state.failureCount,
			TotalDuration: state.totalDuration,
		}
		for i := 0; i < state.count; i++ {
			index := (state.next - state.count + i + recentOperationLimit) % recentOperationLimit
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
		result[name] = value
	}
	return result
}

// GetMetrics returns an independent snapshot of Management-owned operations.
func (layer *Management) GetMetrics(request GetMetricsRequest) (GetMetricsResponse, error) {
	if request.Context == nil {
		return GetMetricsResponse{}, errors.New("metrics context is required")
	}
	if err := request.Context.Err(); err != nil {
		return GetMetricsResponse{}, err
	}
	return GetMetricsResponse{Operations: layer.metrics.snapshot()}, nil
}
