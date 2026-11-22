package management

import (
	"context"
	"fmt"
	"time"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/orchestration"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/platform"
)

// StatusLevel is the coarse health classification returned by GetStatus.
type StatusLevel string

const (
	StatusHealthy  StatusLevel = "healthy"
	StatusDegraded StatusLevel = "degraded"
	StatusFailed   StatusLevel = "failed"
)

// GetStatusRequest requests a low-detail health summary.
type GetStatusRequest struct{ Context context.Context }

// GetStatusResponse contains only lifecycle and aggregate health indicators.
type GetStatusResponse struct {
	Level                 StatusLevel
	Lifecycle             LifecyclePhase
	IntegrityErrorCount   uint64
	RecoveryFailureCount  uint64
	CleanupWorkersStarted bool
	Operations            map[string]OperationMetrics
	DistributionAvailable bool
	DistributionError     string
}

// DriveMonitoringStatus identifies the configured physical drive and reports
// which observations could be retrieved. Unknown values are never fabricated.
type DriveMonitoringStatus struct {
	WWN                         string
	Root                        string
	Mountpoint                  string
	DevicePath                  string
	Device                      physicalio.DeviceStatus
	DeviceStatusAvailable       bool
	DeviceStatusError           string
	DeviceIdentityAvailable     bool
	DeviceIdentityError         string
	Capacity                    shared.Capacity
	CapacityAvailable           bool
	CapacityError               string
	CapacitySource              string
	CapacityRetrievedAt         time.Time
	IOMetrics                   physicalio.GetMetricsResponse
	IOMetricsAvailable          bool
	IOMetricsError              string
	OrphanChunkCount            uint64
	CleanupPermissions          shared.DriveCleanupPermission
	CleanupPermissionsAvailable bool
}

// GetDetailedStatusRequest requests operational monitoring for every configured drive.
type GetDetailedStatusRequest struct{ Context context.Context }

// GetDetailedStatusResponse combines Distribution metrics and per-drive I/O,
// device identity, and filesystem capacity observations.
type GetDetailedStatusResponse struct {
	IOMetricsAggregate       physicalio.GetMetricsResponse
	CapacityAggregate        shared.Capacity
	CapacityDriveCount       uint64
	LogicalFreeBytes         uint64
	LogicalCapacityAvailable bool
	LogicalCapacityError     string
	CleanupWorkersStarted    bool
	Operations               map[string]OperationMetrics
	Status                   GetStatusResponse
	Distribution             distribution.GetMetricsResponse
	DistributionAvailable    bool
	DistributionError        string
	Drives                   []DriveMonitoringStatus
	CollectedAt              time.Time
}

// InspectRequest requests the detailed physical layout audit.
type InspectRequest struct{ Context context.Context }

// InspectResponse contains detailed monitoring plus Distribution's per-object,
// per-stripe, per-version and orphan-chunk inventory.
type InspectResponse struct {
	IOMetricsAggregate       physicalio.GetMetricsResponse
	CapacityAggregate        shared.Capacity
	CapacityDriveCount       uint64
	LogicalFreeBytes         uint64
	LogicalCapacityAvailable bool
	LogicalCapacityError     string
	CleanupWorkersStarted    bool
	Operations               map[string]OperationMetrics
	Namespace                orchestration.InspectNamespaceResponse
	NamespaceAvailable       bool
	NamespaceError           string
	Status                   GetStatusResponse
	Distribution             distribution.GetMetricsResponse
	DistributionAvailable    bool
	DistributionError        string
	Drives                   []DriveMonitoringStatus
	CollectedAt              time.Time
}

func validateMonitoringContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return ctx.Err()
}

// statusFromMetrics converts a Distribution snapshot into the coarse health view.
func statusFromMetrics(phase LifecyclePhase, metrics distribution.GetMetricsResponse) GetStatusResponse {
	result := GetStatusResponse{
		Lifecycle: phase, Level: StatusHealthy,
		IntegrityErrorCount:  metrics.IntegrityErrorCount,
		RecoveryFailureCount: metrics.RecoveryFailureCount,
	}
	if phase == LifecycleFailed {
		result.Level = StatusFailed
	} else if phase != LifecycleReady || result.IntegrityErrorCount > 0 || result.RecoveryFailureCount > 0 {
		result.Level = StatusDegraded
	}
	return result
}

// GetStatus provides a compact health summary. It deliberately avoids per-drive
// device and capacity probes; those belong to GetDetailedStatus.
func (layer *Management) GetStatus(request GetStatusRequest) (response GetStatusResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("GetStatus", started, err) }()
	if err := validateMonitoringContext(request.Context); err != nil {
		return GetStatusResponse{}, err
	}
	phase := layer.Phase()
	result := statusFromMetrics(phase, distribution.GetMetricsResponse{})
	result.Operations = layer.metrics.snapshot()
	if layer.distribution == nil {
		result.DistributionError = "Distribution layer is not initialized"
	} else {
		metrics, metricsErr := layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context, SummaryOnly: true})
		if metricsErr != nil {
			result.DistributionError = metricsErr.Error()
		} else {
			result.DistributionAvailable = true
			result.IntegrityErrorCount = metrics.IntegrityErrorCount
			result.RecoveryFailureCount = metrics.RecoveryFailureCount
			result = statusFromMetrics(phase, metrics)
			result.Operations = layer.metrics.snapshot()
			result.DistributionAvailable = true
		}
	}
	layer.mu.Lock()
	result.CleanupWorkersStarted = layer.backgroundProcessesStarted
	layer.mu.Unlock()
	if layer.Phase() == LifecycleReady && (!result.CleanupWorkersStarted || !result.DistributionAvailable) {
		result.Level = StatusDegraded
	}
	return result, nil
}

// GetDetailedStatus gathers a current snapshot from each layer. A failure to
// observe one drive is represented on that drive and does not hide other drives.
func (layer *Management) GetDetailedStatus(request GetDetailedStatusRequest) (response GetDetailedStatusResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("GetDetailedStatus", started, err) }()
	if err := validateMonitoringContext(request.Context); err != nil {
		return GetDetailedStatusResponse{}, err
	}
	result := GetDetailedStatusResponse{CollectedAt: time.Now(), Drives: make([]DriveMonitoringStatus, 0, len(layer.driveOrder)), Operations: layer.metrics.snapshot()}
	result.Status = statusFromMetrics(layer.Phase(), distribution.GetMetricsResponse{})
	if layer.distribution == nil {
		result.DistributionError = "Distribution layer is not initialized"
	} else {
		metrics, metricsErr := layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context})
		if metricsErr != nil {
			result.DistributionError = metricsErr.Error()
		} else {
			result.Distribution = metrics
			result.DistributionAvailable = true
			result.Status = statusFromMetrics(layer.Phase(), metrics)
		}
	}
	layer.mu.Lock()
	result.CleanupWorkersStarted = layer.backgroundProcessesStarted
	result.Status.CleanupWorkersStarted = result.CleanupWorkersStarted
	result.Status.DistributionAvailable = result.DistributionAvailable
	result.Status.DistributionError = result.DistributionError
	if layer.Phase() == LifecycleReady && (!result.CleanupWorkersStarted || !result.DistributionAvailable) {
		result.Status.Level = StatusDegraded
	}
	orchestrationLayer := layer.orchestration
	if orchestrationLayer != nil {
		statfs, statErr := orchestrationLayer.StatFsLocked(orchestration.StatFsRequest{Context: request.Context})
		if statErr != nil {
			result.LogicalCapacityError = statErr.Error()
		} else {
			result.LogicalFreeBytes = statfs.Bavail * statfs.Bsize
			result.LogicalCapacityAvailable = true
		}
	} else {
		result.LogicalCapacityError = "Orchestration layer is not initialized"
	}
	permissionSnapshot := layer.cleanupPermissionStore.Snapshot()
	layer.mu.Unlock()
	for index, wwn := range layer.driveOrder {
		if err := request.Context.Err(); err != nil {
			return GetDetailedStatusResponse{}, err
		}
		config := layer.drives[wwn]
		drive := DriveMonitoringStatus{WWN: wwn, Root: config.Root, Mountpoint: config.Mountpoint}
		for _, orphan := range result.Distribution.OrphanChunksByDrive {
			if orphan.DriveIndex == index {
				drive.OrphanChunkCount = orphan.OrphanChunkCount
				break
			}
		}
		if index < len(permissionSnapshot.Drives) {
			drive.CleanupPermissions = permissionSnapshot.Drives[index]
			drive.CleanupPermissionsAvailable = true
		}
		if index < len(layer.ios) && layer.ios[index] != nil {
			device, deviceErr := layer.ios[index].GetDeviceStatus(physicalio.GetDeviceStatusRequest{Context: request.Context})
			if deviceErr != nil {
				drive.DeviceStatusError = deviceErr.Error()
				drive.DeviceIdentityError = deviceErr.Error()
			} else {
				drive.Device = device.Status
				drive.DeviceStatusAvailable = true
				drive.DevicePath = device.Status.DevicePath
				drive.DeviceIdentityAvailable = drive.DevicePath != ""
			}
		} else {
			drive.DeviceStatusError = "I/O device status source is not configured for this drive"
			drive.DeviceIdentityError = drive.DeviceStatusError
		}
		capacity, capacityErr := platform.GetCapacity(platform.CapacityRequest{Context: request.Context, Root: config.Root})
		if capacityErr != nil {
			drive.CapacityError = capacityErr.Error()
		} else {
			drive.Capacity = capacity.Capacity
			drive.CapacityAvailable = true
			drive.CapacitySource = "platform.GetCapacity"
			drive.CapacityRetrievedAt = time.Now()
		}
		if index < len(layer.ios) && layer.ios[index] != nil {
			drive.IOMetrics, capacityErr = layer.ios[index].GetMetrics(physicalio.GetMetricsRequest{Context: request.Context})
			if capacityErr != nil {
				drive.IOMetricsError = capacityErr.Error()
			} else {
				drive.IOMetricsAvailable = true
			}
		} else {
			drive.IOMetricsError = "I/O metrics source is not configured for this drive"
		}
		if !drive.DeviceIdentityAvailable || !drive.CapacityAvailable || !drive.IOMetricsAvailable {
			if result.Status.Level == StatusHealthy {
				result.Status.Level = StatusDegraded
			}
		}
		result.Drives = append(result.Drives, drive)
	}
	result.IOMetricsAggregate = aggregateIOMetrics(result.Drives)
	result.CapacityAggregate, result.CapacityDriveCount = aggregateCapacity(result.Drives)
	return result, nil
}

// Inspect performs the detailed monitoring collection and includes the
// Distribution-owned physical inventory. It does not wake standby drives.
func (layer *Management) Inspect(request InspectRequest) (response InspectResponse, err error) {
	started := time.Now()
	defer func() { layer.recordOperation("Inspect", started, err) }()
	if err := validateMonitoringContext(request.Context); err != nil {
		return InspectResponse{}, err
	}
	result := InspectResponse{CollectedAt: time.Now(), Drives: make([]DriveMonitoringStatus, 0, len(layer.driveOrder)), Operations: layer.metrics.snapshot()}
	result.Status = statusFromMetrics(layer.Phase(), distribution.GetMetricsResponse{})
	if layer.distribution == nil {
		result.DistributionError = "Distribution layer is not initialized"
	} else {
		metrics, metricsErr := layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context, IncludeDetails: true})
		if metricsErr != nil {
			result.DistributionError = metricsErr.Error()
		} else {
			result.Distribution = metrics
			result.DistributionAvailable = true
			result.Status = statusFromMetrics(layer.Phase(), metrics)
		}
	}
	layer.mu.Lock()
	result.CleanupWorkersStarted = layer.backgroundProcessesStarted
	result.Status.CleanupWorkersStarted = result.CleanupWorkersStarted
	result.Status.DistributionAvailable = result.DistributionAvailable
	result.Status.DistributionError = result.DistributionError
	if layer.Phase() == LifecycleReady && (!result.CleanupWorkersStarted || !result.DistributionAvailable) {
		result.Status.Level = StatusDegraded
	}
	permissionSnapshot := layer.cleanupPermissionStore.Snapshot()
	orchestrationLayer := layer.orchestration
	if orchestrationLayer != nil {
		namespace, namespaceErr := orchestrationLayer.InspectNamespace(orchestration.InspectNamespaceRequest{Context: request.Context})
		if namespaceErr != nil {
			result.NamespaceError = namespaceErr.Error()
		} else {
			result.Namespace = namespace
			result.NamespaceAvailable = true
		}
		statfs, statErr := orchestrationLayer.StatFsLocked(orchestration.StatFsRequest{Context: request.Context})
		if statErr != nil {
			result.LogicalCapacityError = statErr.Error()
		} else {
			result.LogicalFreeBytes = statfs.Bavail * statfs.Bsize
			result.LogicalCapacityAvailable = true
		}
	} else {
		result.NamespaceError = "Orchestration layer is not initialized"
		result.LogicalCapacityError = result.NamespaceError
	}
	layer.mu.Unlock()
	for _, orphan := range result.Distribution.OrphanChunks {
		if orphan.DriveIndex >= 0 && orphan.DriveIndex < len(layer.driveOrder) {
			_ = orphan
		}
	}
	for index, wwn := range layer.driveOrder {
		if err := request.Context.Err(); err != nil {
			return InspectResponse{}, err
		}
		config := layer.drives[wwn]
		drive := DriveMonitoringStatus{WWN: wwn, Root: config.Root, Mountpoint: config.Mountpoint}
		for _, orphan := range result.Distribution.OrphanChunksByDrive {
			if orphan.DriveIndex == index {
				drive.OrphanChunkCount = orphan.OrphanChunkCount
				break
			}
		}
		if index < len(permissionSnapshot.Drives) {
			drive.CleanupPermissions = permissionSnapshot.Drives[index]
			drive.CleanupPermissionsAvailable = true
		}
		for _, orphan := range result.Distribution.OrphanChunks {
			if orphan.DriveIndex == index {
				drive.OrphanChunkCount++
			}
		}
		if index < len(layer.ios) && layer.ios[index] != nil {
			device, deviceErr := layer.ios[index].GetDeviceStatus(physicalio.GetDeviceStatusRequest{Context: request.Context})
			if deviceErr != nil {
				drive.DeviceStatusError = deviceErr.Error()
				drive.DeviceIdentityError = deviceErr.Error()
			} else {
				drive.Device = device.Status
				drive.DeviceStatusAvailable = true
				drive.DevicePath = device.Status.DevicePath
				drive.DeviceIdentityAvailable = drive.DevicePath != ""
			}
		} else {
			drive.DeviceStatusError = "I/O device status source is not configured for this drive"
			drive.DeviceIdentityError = drive.DeviceStatusError
		}
		capacity, capacityErr := platform.GetCapacity(platform.CapacityRequest{Context: request.Context, Root: config.Root})
		if capacityErr != nil {
			drive.CapacityError = capacityErr.Error()
		} else {
			drive.Capacity = capacity.Capacity
			drive.CapacityAvailable = true
			drive.CapacitySource = "platform.GetCapacity"
			drive.CapacityRetrievedAt = time.Now()
		}
		if index < len(layer.ios) && layer.ios[index] != nil {
			drive.IOMetrics, capacityErr = layer.ios[index].GetMetrics(physicalio.GetMetricsRequest{Context: request.Context})
			if capacityErr != nil {
				drive.IOMetricsError = capacityErr.Error()
			} else {
				drive.IOMetricsAvailable = true
			}
		} else {
			drive.IOMetricsError = "I/O metrics source is not configured for this drive"
		}
		result.Drives = append(result.Drives, drive)
		if (!drive.DeviceIdentityAvailable || !drive.CapacityAvailable || !drive.IOMetricsAvailable) && result.Status.Level == StatusHealthy {
			result.Status.Level = StatusDegraded
		}
	}
	result.IOMetricsAggregate = aggregateIOMetrics(result.Drives)
	result.CapacityAggregate, result.CapacityDriveCount = aggregateCapacity(result.Drives)
	return result, nil
}

func aggregateIOMetrics(drives []DriveMonitoringStatus) physicalio.GetMetricsResponse {
	result := physicalio.GetMetricsResponse{Operations: make(map[physicalio.Operation]physicalio.OperationMetrics)}
	for _, drive := range drives {
		if !drive.IOMetricsAvailable {
			continue
		}
		for operation, value := range drive.IOMetrics.Operations {
			total := result.Operations[operation]
			total.SuccessCount += value.SuccessCount
			total.FailureCount += value.FailureCount
			total.ReadErrorCount += value.ReadErrorCount
			total.UniqueReadErrorFileCount += value.UniqueReadErrorFileCount
			total.TotalDuration += value.TotalDuration
			total.Recent.ProcessedCount += value.Recent.ProcessedCount
			total.Recent.SuccessCount += value.Recent.SuccessCount
			total.Recent.FailureCount += value.Recent.FailureCount
			total.Recent.ReadErrorCount += value.Recent.ReadErrorCount
			total.Recent.TotalDuration += value.Recent.TotalDuration
			if value.Recent.MaxDuration > total.Recent.MaxDuration {
				total.Recent.MaxDuration = value.Recent.MaxDuration
			}
			result.Operations[operation] = total
		}
	}
	return result
}

func aggregateCapacity(drives []DriveMonitoringStatus) (shared.Capacity, uint64) {
	var result shared.Capacity
	var count uint64
	for _, drive := range drives {
		if !drive.CapacityAvailable {
			continue
		}
		result.TotalBytes = saturatingAddBytes(result.TotalBytes, drive.Capacity.TotalBytes)
		result.FreeBytes = saturatingAddBytes(result.FreeBytes, drive.Capacity.FreeBytes)
		result.AvailableBytes = saturatingAddBytes(result.AvailableBytes, drive.Capacity.AvailableBytes)
		count++
	}
	return result, count
}

func saturatingAddBytes(a, b uint64) uint64 {
	const maxUint64 = ^uint64(0)
	if maxUint64-a < b {
		return maxUint64
	}
	return a + b
}
