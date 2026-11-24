package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

const recentMetricsLimit = 1000

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
	ErrorTypes     map[string]uint64
}

type ReadErrorMetrics struct {
	OccurrenceCount   uint64
	UniqueObjectCount uint64
	ByObject          []ObjectReadErrorMetrics
	Recent            RecentReadErrorMetrics
}

type ObjectReadErrorMetrics struct {
	ObjectID        shared.ObjectID
	OccurrenceCount uint64
}

type RecentReadErrorMetrics struct {
	OccurrenceCount   uint64
	UniqueObjectCount uint64
	ErrorTypes        map[string]uint64
}

type GetMetricsRequest struct {
	Context context.Context
}

type GetMetricsResponse struct {
	Operations                    map[string]OperationMetrics
	ReadErrors                    ReadErrorMetrics
	RecoverableMissingFileCount   uint64
	UnrecoverableMissingFileCount uint64
}

type operationSample struct {
	success   bool
	duration  time.Duration
	errorType string
}

type operationMetricState struct {
	successCount  uint64
	failureCount  uint64
	totalDuration time.Duration
	samples       [recentMetricsLimit]operationSample
	next          int
	count         int
}

type readErrorSample struct {
	objectID  shared.ObjectID
	errorType string
}

type metricsCollector struct {
	mu                        sync.RWMutex
	operations                map[string]*operationMetricState
	readErrorOccurrences      uint64
	readErrorObjects          map[shared.ObjectID]uint64
	readErrorSamples          [recentMetricsLimit]readErrorSample
	readErrorNext             int
	readErrorCount            int
	recoverableMissingFiles   map[shared.ObjectID]struct{}
	unrecoverableMissingFiles map[shared.ObjectID]struct{}
}

func newMetricsCollector() *metricsCollector {
	collector := &metricsCollector{
		operations:                make(map[string]*operationMetricState),
		readErrorObjects:          make(map[shared.ObjectID]uint64),
		recoverableMissingFiles:   make(map[shared.ObjectID]struct{}),
		unrecoverableMissingFiles: make(map[shared.ObjectID]struct{}),
	}
	for _, name := range []string{
		"Create", "Mkdir", "Symlink", "Link", "Unlink", "Rmdir", "Rename",
		"Lookup", "GetAttr", "ReadDir", "Open", "Release", "Read", "Write",
		"Truncate", "SetAttr", "ReadLink", "Flush", "Fsync", "StatFs", "Cleanup",
		"InspectNamespace", "GetMetrics", "StartCleanupWorker", "StopCleanupWorker", "SetCleanupPermissionStore",
	} {
		collector.operations[name] = &operationMetricState{}
	}
	return collector
}

func (c *metricsCollector) recordOperation(name string, duration time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.operations[name]
	if state == nil {
		state = &operationMetricState{}
		c.operations[name] = state
	}
	if err == nil {
		state.successCount++
	} else {
		state.failureCount++
	}
	state.totalDuration += duration
	state.samples[state.next] = operationSample{success: err == nil, duration: duration, errorType: metricErrorType(err)}
	state.next = (state.next + 1) % recentMetricsLimit
	if state.count < recentMetricsLimit {
		state.count++
	}
}

func (layer *Orchestration) recordOperation(name string, started time.Time, err error) {
	if layer.trace != nil {
		layer.trace.Record("Orchestration."+name, started, err)
	}
	if layer.metrics == nil {
		return
	}
	layer.metrics.recordOperation(name, time.Since(started), err)
}

func (layer *Orchestration) recordReadError(objectID shared.ObjectID, err error) {
	if err == nil || layer.metrics == nil {
		return
	}
	collector := layer.metrics
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	collector.readErrorOccurrences++
	collector.readErrorObjects[objectID]++
	collector.readErrorSamples[collector.readErrorNext] = readErrorSample{objectID: objectID, errorType: metricErrorType(err)}
	collector.readErrorNext = (collector.readErrorNext + 1) % recentMetricsLimit
	if collector.readErrorCount < recentMetricsLimit {
		collector.readErrorCount++
	}
}

func metricErrorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, distribution.ErrDataCorrupted):
		return "data_corrupted"
	case errors.Is(err, distribution.ErrStripeNotFound):
		return "stripe_not_found"
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "permission"
	case errors.Is(err, syscall.ENOSPC):
		return "no_space"
	case errors.Is(err, os.ErrInvalid), errors.Is(err, syscall.EINVAL):
		return "invalid"
	default:
		return fmt.Sprintf("%T", err)
	}
}

func (c *metricsCollector) snapshot() (map[string]OperationMetrics, ReadErrorMetrics) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	operations := make(map[string]OperationMetrics, len(c.operations))
	for name, state := range c.operations {
		value := OperationMetrics{
			SuccessCount:  state.successCount,
			FailureCount:  state.failureCount,
			TotalDuration: state.totalDuration,
			Recent:        RecentOperationMetrics{ErrorTypes: make(map[string]uint64)},
		}
		for i := 0; i < state.count; i++ {
			index := (state.next - state.count + i + recentMetricsLimit) % recentMetricsLimit
			sample := state.samples[index]
			value.Recent.ProcessedCount++
			value.Recent.TotalDuration += sample.duration
			if sample.success {
				value.Recent.SuccessCount++
			} else {
				value.Recent.FailureCount++
				value.Recent.ErrorTypes[sample.errorType]++
			}
			if sample.duration > value.Recent.MaxDuration {
				value.Recent.MaxDuration = sample.duration
			}
		}
		operations[name] = value
	}

	readErrors := ReadErrorMetrics{
		OccurrenceCount:   c.readErrorOccurrences,
		UniqueObjectCount: uint64(len(c.readErrorObjects)),
		ByObject:          make([]ObjectReadErrorMetrics, 0, len(c.readErrorObjects)),
		Recent:            RecentReadErrorMetrics{ErrorTypes: make(map[string]uint64)},
	}
	for objectID, count := range c.readErrorObjects {
		readErrors.ByObject = append(readErrors.ByObject, ObjectReadErrorMetrics{ObjectID: objectID, OccurrenceCount: count})
	}
	sort.Slice(readErrors.ByObject, func(i, j int) bool {
		return readErrors.ByObject[i].ObjectID.String() < readErrors.ByObject[j].ObjectID.String()
	})
	recentObjects := make(map[shared.ObjectID]struct{})
	for i := 0; i < c.readErrorCount; i++ {
		index := (c.readErrorNext - c.readErrorCount + i + recentMetricsLimit) % recentMetricsLimit
		sample := c.readErrorSamples[index]
		readErrors.Recent.OccurrenceCount++
		recentObjects[sample.objectID] = struct{}{}
		readErrors.Recent.ErrorTypes[sample.errorType]++
	}
	readErrors.Recent.UniqueObjectCount = uint64(len(recentObjects))
	return operations, readErrors
}

// refreshMissingFileStates rebuilds the non-persistent logical missing-file index
// from the Distribution-owned physical Stripe/Version snapshot.
func (layer *Orchestration) refreshMissingFileStates(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	metrics, err := layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: ctx, IncludeDetails: true})
	if err != nil {
		return fmt.Errorf("get Distribution metrics: %w", err)
	}
	objects := make(map[shared.ObjectID]distribution.ObjectMetrics, len(metrics.Objects))
	for _, object := range metrics.Objects {
		objects[object.ObjectID] = object
	}
	layer.rebuildMissingLogicalFileStates(objects)
	return nil
}

// GetMetrics returns Orchestration-owned logical operation and availability metrics.
// Logical missing-file state is derived from Distribution's Stripe/Version snapshot.
func (layer *Orchestration) GetMetrics(request GetMetricsRequest) (response GetMetricsResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("GetMetrics", started, err) }()
	if request.Context == nil {
		return response, fmt.Errorf("context is required")
	}
	if err = request.Context.Err(); err != nil {
		return response, err
	}
	distributionMetrics, err := layer.distribution.GetMetrics(distribution.GetMetricsRequest{
		Context: request.Context, IncludeDetails: true,
	})
	if err != nil {
		return GetMetricsResponse{}, fmt.Errorf("get Distribution metrics: %w", err)
	}
	if err = request.Context.Err(); err != nil {
		return GetMetricsResponse{}, err
	}
	objectMetrics := make(map[shared.ObjectID]distribution.ObjectMetrics, len(distributionMetrics.Objects))
	for _, object := range distributionMetrics.Objects {
		objectMetrics[object.ObjectID] = object
	}
	layer.rebuildMissingLogicalFileStates(objectMetrics)
	response.Operations, response.ReadErrors = layer.metrics.snapshot()
	layer.metrics.mu.RLock()
	response.RecoverableMissingFileCount = uint64(len(layer.metrics.recoverableMissingFiles))
	response.UnrecoverableMissingFileCount = uint64(len(layer.metrics.unrecoverableMissingFiles))
	layer.metrics.mu.RUnlock()
	return response, nil
}

func (layer *Orchestration) rebuildMissingLogicalFileStates(
	distributionObjects map[shared.ObjectID]distribution.ObjectMetrics,
) {
	recoverableFiles := make(map[shared.ObjectID]struct{})
	unrecoverableFiles := make(map[shared.ObjectID]struct{})
	reachableFiles := make(map[shared.ObjectID]struct{})
	queue := []*runtimeObject{layer.root}
	visited := make(map[shared.ObjectID]struct{})
	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		if object == nil || object.header.Deleted {
			continue
		}
		if _, exists := visited[object.header.ObjectID]; exists {
			continue
		}
		visited[object.header.ObjectID] = struct{}{}
		switch object.header.Kind {
		case objectKindFile:
			reachableFiles[object.header.ObjectID] = struct{}{}
		case objectKindHardLink:
			if object.file != nil {
				reachableFiles[object.file.objectID] = struct{}{}
			}
		case objectKindDirectory:
			for _, child := range object.children {
				queue = append(queue, child)
			}
		}
	}

	for objectID := range reachableFiles {
		object := layer.objects[objectID]
		if object == nil || object.file == nil {
			continue
		}
		missing, recoverable := logicalFileMissingState(object.file.header, distributionObjects[objectID])
		if !missing {
			continue
		}
		if recoverable {
			recoverableFiles[objectID] = struct{}{}
		} else {
			unrecoverableFiles[objectID] = struct{}{}
		}
	}
	layer.metrics.mu.Lock()
	layer.metrics.recoverableMissingFiles = recoverableFiles
	layer.metrics.unrecoverableMissingFiles = unrecoverableFiles
	layer.metrics.mu.Unlock()
}

func logicalFileMissingState(fileHeader header, metrics distribution.ObjectMetrics) (missing, recoverable bool) {
	first, err := geometryFor(fileHeader, 0)
	if err != nil {
		return true, false
	}
	expectedStripes := uint64(1)
	if fileHeader.Size > first.Capacity {
		if first.Layout.Type == distribution.LayoutMirror || fileHeader.Layout.ChunkSize == 0 {
			return true, false
		}
		regularCapacity := uint64(fileHeader.Layout.ChunkSize) * uint64(first.Layout.NumChunks)
		if regularCapacity == 0 {
			return true, false
		}
		remaining := fileHeader.Size - first.Capacity
		expectedStripes += 1 + (remaining-1)/regularCapacity
	}
	byStripe := make(map[storage.StripeID][]distribution.StripeVersionMetrics)
	for _, stripe := range metrics.Stripes {
		byStripe[stripe.StripeID] = append(byStripe[stripe.StripeID], stripe)
	}
	for stripeID := uint64(0); stripeID < expectedStripes; stripeID++ {
		versions := byStripe[storage.StripeID(stripeID)]
		var current *distribution.StripeVersionMetrics
		for i := range versions {
			if versions[i].Current {
				current = &versions[i]
				break
			}
		}
		if current == nil {
			for i := range versions {
				if versions[i].Recoverable && !versions[i].Invalid {
					return true, true
				}
			}
			return true, false
		}
		if current.PresentChunkCount < current.ExpectedChunkCount || len(current.MissingChunkIndexes) > 0 || current.Corrupted {
			missing = true
			if !current.Recoverable {
				return true, false
			}
			recoverable = true
		}
	}
	return missing, recoverable
}
