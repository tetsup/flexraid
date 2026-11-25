package distribution

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

const recentOperationLimit = 1000

type OperationMetrics struct {
	SuccessCount  uint64
	FailureCount  uint64
	TotalDuration time.Duration
	MinDuration   time.Duration
	MaxDuration   time.Duration
	Recent        RecentOperationMetrics
}
type RecentOperationMetrics struct {
	ProcessedCount uint64
	SuccessCount   uint64
	FailureCount   uint64
	TotalDuration  time.Duration
	MinDuration    time.Duration
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
type GetMetricsRequest struct {
	Context        context.Context
	IncludeDetails bool
	SummaryOnly    bool
}
type ObjectMetrics struct {
	ObjectID         shared.ObjectID
	StripeCount      uint64
	OrphanChunkCount uint64
	Stripes          []StripeVersionMetrics
}
type StripeVersionMetrics struct {
	StripeID            storage.StripeID
	Version             storage.StripeVersion
	Layout              Layout
	ChunkSize           uint32
	ExpectedChunkCount  uint64
	PresentChunkCount   uint64
	MissingChunkIndexes []uint8
	Invalid             bool
	Recoverable         bool
	Current             bool
	Corrupted           bool
}
type OrphanChunkMetrics struct {
	DriveIndex int
	Key        storage.FileKey
}
type DriveOrphanMetrics struct {
	DriveIndex       int
	OrphanChunkCount uint64
}
type CacheMemoryMetrics struct {
	ObjectIndexEntries     uint64
	StripeIndexEntries     uint64
	VersionIndexEntries    uint64
	OrphanChunkEntries     uint64
	StripeLockEntries      uint64
	CleanupPendingEntries  uint64
	CleanupInFlightEntries uint64
	CachedChunkCount       uint64
	CachedChunkBytes       uint64
}

type GetMetricsResponse struct {
	CacheMemory CacheMemoryMetrics
	Objects                            []ObjectMetrics
	OrphanChunks                       []OrphanChunkMetrics
	OrphanChunksByDrive                []DriveOrphanMetrics
	CleanupCandidates                  []CleanupCandidateMetrics
	CleanupAfterPhysicalAvailableBytes []DriveCleanupCapacityMetrics
	CleanupAfterMirrorAvailableBlocks  uint64
	CleanupAfterParityAvailableBlocks  uint64
	CleanupAfterCapacityAvailable      bool
	CleanupAfterCapacityError          string
	Operations                         map[string]OperationMetrics
	Chunks                             ChunkMetrics
	Stripes                            StripeMetrics
	IntegrityErrorCount                uint64
	RecoveryCount                      uint64
	RecoveryFailureCount               uint64
}
type operationSample struct {
	success  bool
	duration time.Duration
}
type operationMetricState struct {
	successCount  uint64
	failureCount  uint64
	totalDuration time.Duration
	minDuration   time.Duration
	maxDuration   time.Duration
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
	collector := &metricsCollector{operations: make(map[string]*operationMetricState), chunks: make(map[string]*chunkMetricState)}
	for _, name := range []string{"ReadStripe", "WriteStripe", "Cleanup", "CleanupStripeDrive", "DeleteObject", "DeleteObjects", "ListObjects", "GetStripeInfo", "ListStripeInfos", "SyncObject", "GetCapacity", "StartCleanupWorker", "StopCleanupWorker", "SetCleanupPermissionStore", "ObjectDriveIndexes", "NotifyCleanupPermissionsChanged"} {
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
	if state.successCount+state.failureCount == 0 || duration < state.minDuration {
		state.minDuration = duration
	}
	if duration > state.maxDuration {
		state.maxDuration = duration
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
	collector := d.metricsCollector()
	collector.mu.Lock()
	defer collector.mu.Unlock()
	value := collector.chunks[kind]
	if value == nil {
		value = &chunkMetricState{}
		collector.chunks[kind] = value
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
	collector := d.metricsCollector()
	collector.mu.Lock()
	collector.integrityErrorCount++
	collector.mu.Unlock()
}

func (d *Distribution) recordRecovery(success bool) {
	collector := d.metricsCollector()
	collector.mu.Lock()
	if success {
		collector.recoveryCount++
	} else {
		collector.recoveryFailureCount++
	}
	collector.mu.Unlock()
}

func (c *metricsCollector) snapshot() (map[string]OperationMetrics, ChunkMetrics, uint64, uint64, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	operations := make(map[string]OperationMetrics, len(c.operations))
	for name, state := range c.operations {
		value := OperationMetrics{SuccessCount: state.successCount, FailureCount: state.failureCount, TotalDuration: state.totalDuration, MinDuration: state.minDuration, MaxDuration: state.maxDuration}
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
			if value.Recent.ProcessedCount == 1 || sample.duration < value.Recent.MinDuration {
				value.Recent.MinDuration = sample.duration
			}
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
	response.Operations, response.Chunks, response.IntegrityErrorCount, response.RecoveryCount, response.RecoveryFailureCount = d.metricsCollector().snapshot()
	if request.SummaryOnly {
		return response, nil
	}
	d.mapMu.RLock()
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
	driveOrphans := make(map[int]uint64)
	for objectID, locations := range d.orphans {
		objects[objectID] = struct{}{}
		response.Stripes.OrphanChunkCount += uint64(len(locations))
		for _, location := range locations {
			driveOrphans[location.ioIndex]++
		}
		for _, location := range locations {
			stripes[stripeIDKey{objectID: objectID, stripeID: location.key.StripeID}] = struct{}{}
		}
	}
	for driveIndex, count := range driveOrphans {
		response.OrphanChunksByDrive = append(response.OrphanChunksByDrive, DriveOrphanMetrics{DriveIndex: driveIndex, OrphanChunkCount: count})
	}
	sort.Slice(response.OrphanChunksByDrive, func(i, j int) bool {
		return response.OrphanChunksByDrive[i].DriveIndex < response.OrphanChunksByDrive[j].DriveIndex
	})
	if request.IncludeDetails {
		objectIDs := make([]shared.ObjectID, 0, len(objects))
		for id := range objects {
			objectIDs = append(objectIDs, id)
		}
		sort.Slice(objectIDs, func(i, j int) bool { return objectIDs[i].String() < objectIDs[j].String() })
		for _, objectID := range objectIDs {
			object := ObjectMetrics{ObjectID: objectID}
			for _, orphan := range d.orphans[objectID] {
				object.OrphanChunkCount++
				response.OrphanChunks = append(response.OrphanChunks, OrphanChunkMetrics{DriveIndex: orphan.ioIndex, Key: orphan.key})
			}
			for key, versions := range d.versions {
				if key.objectID != objectID {
					continue
				}
				object.StripeCount++
				current := d.objects[objectID][key.stripeID]
				versionIDs := make([]storage.StripeVersion, 0, len(versions))
				for versionID := range versions {
					versionIDs = append(versionIDs, versionID)
				}
				sort.Slice(versionIDs, func(i, j int) bool { return versionIDs[i] < versionIDs[j] })
				for _, versionID := range versionIDs {
					version := versions[versionID]
					entry := StripeVersionMetrics{StripeID: key.stripeID, Version: versionID, Layout: version.method.layout(), ChunkSize: version.chunkSize, ExpectedChunkCount: uint64(len(expectedChunkNos(version.method))), PresentChunkCount: uint64(len(version.chunks)), Invalid: version.invalid, Recoverable: version.recoverable, Current: current != nil && current.version == versionID, Corrupted: version.invalid || !version.recoverable || (current != nil && current.version == versionID && current.corrupted)}
					for missing := range version.missing {
						entry.MissingChunkIndexes = append(entry.MissingChunkIndexes, uint8(missing))
					}
					sort.Slice(entry.MissingChunkIndexes, func(i, j int) bool { return entry.MissingChunkIndexes[i] < entry.MissingChunkIndexes[j] })
					object.Stripes = append(object.Stripes, entry)
				}
			}
			sort.Slice(object.Stripes, func(i, j int) bool {
				if object.Stripes[i].StripeID != object.Stripes[j].StripeID {
					return object.Stripes[i].StripeID < object.Stripes[j].StripeID
				}
				return object.Stripes[i].Version < object.Stripes[j].Version
			})
			response.Objects = append(response.Objects, object)
		}
		sort.Slice(response.OrphanChunks, func(i, j int) bool {
			if response.OrphanChunks[i].DriveIndex != response.OrphanChunks[j].DriveIndex {
				return response.OrphanChunks[i].DriveIndex < response.OrphanChunks[j].DriveIndex
			}
			if response.OrphanChunks[i].Key.ObjectID != response.OrphanChunks[j].Key.ObjectID {
				return response.OrphanChunks[i].Key.ObjectID.String() < response.OrphanChunks[j].Key.ObjectID.String()
			}
			if response.OrphanChunks[i].Key.StripeID != response.OrphanChunks[j].Key.StripeID {
				return response.OrphanChunks[i].Key.StripeID < response.OrphanChunks[j].Key.StripeID
			}
			return response.OrphanChunks[i].Key.StripeVersion < response.OrphanChunks[j].Key.StripeVersion
		})
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
	response.CleanupCandidates = d.cleanupCandidateMetricsLocked()
	d.mapMu.RUnlock()

	capacity, capacityErr := d.getCapacity(CapacityRequest{Context: request.Context})
	if capacityErr != nil {
		response.CleanupAfterCapacityError = capacityErr.Error()
	} else {
		availableBlocks := make([]uint64, len(capacity.Capacities))
		response.CleanupAfterPhysicalAvailableBytes = make([]DriveCleanupCapacityMetrics, len(capacity.Capacities))
		for index, drive := range capacity.Capacities {
			var reclaimable uint64
			for _, candidate := range response.CleanupCandidates {
				if candidate.DriveIndex == index {
					reclaimable = saturatingAdd(reclaimable, candidate.ReclaimableBytes)
				}
			}
			available := saturatingAdd(drive.AvailableBytes, reclaimable)
			availableBlocks[index] = available / config.BlockSize
			response.CleanupAfterPhysicalAvailableBytes[index] = DriveCleanupCapacityMetrics{
				DriveIndex: index, CurrentAvailableBytes: drive.AvailableBytes,
				ReclaimableBytes: reclaimable, EstimatedAvailableBytes: available,
			}
		}
		response.CleanupAfterMirrorAvailableBlocks, response.CleanupAfterParityAvailableBlocks =
			estimateLogicalCapacityBlocks(availableBlocks, config.BlockSize, 2, config.DefaultDataChunks, config.DefaultParityChunks)
		response.CleanupAfterCapacityAvailable = true
	}
	return response, nil
}
