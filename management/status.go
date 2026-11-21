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
	Level                    StatusLevel
	Lifecycle                LifecyclePhase
	ObjectCount              uint64
	StripeCount              uint64
	UnrecoverableStripeCount uint64
	CorruptedStripeCount     uint64
	MissingChunkCount        uint64
	OrphanChunkCount         uint64
	IntegrityErrorCount      uint64
	RecoveryFailureCount     uint64
}

// DriveMonitoringStatus identifies the configured physical drive and reports
// which observations could be retrieved. Unknown values are never fabricated.
type DriveMonitoringStatus struct {
	WWN                     string
	Root                    string
	Mountpoint              string
	DevicePath              string
	DeviceIdentityAvailable bool
	DeviceIdentityError     string
	Capacity                shared.Capacity
	CapacityAvailable       bool
	CapacityError           string
	CapacitySource          string
	CapacityRetrievedAt     time.Time
	IOMetrics               physicalio.GetMetricsResponse
	IOMetricsAvailable      bool
	IOMetricsError          string
	OrphanChunkCount        uint64
}

// GetDetailedStatusRequest requests operational monitoring for every configured drive.
type GetDetailedStatusRequest struct{ Context context.Context }

// GetDetailedStatusResponse combines Distribution metrics and per-drive I/O,
// device identity, and filesystem capacity observations.
type GetDetailedStatusResponse struct {
	Status       GetStatusResponse
	Distribution distribution.GetMetricsResponse
	Drives       []DriveMonitoringStatus
	CollectedAt  time.Time
}

// InspectRequest requests the detailed physical layout audit.
type InspectRequest struct{ Context context.Context }

// InspectResponse contains detailed monitoring plus Distribution's per-object,
// per-stripe, per-version and orphan-chunk inventory.
type InspectResponse struct {
	Namespace          orchestration.InspectNamespaceResponse
	NamespaceAvailable bool
	NamespaceError     string
	Status             GetStatusResponse
	Distribution       distribution.GetMetricsResponse
	Drives             []DriveMonitoringStatus
	CollectedAt        time.Time
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
		ObjectCount: metrics.Stripes.ObjectCount, StripeCount: metrics.Stripes.StripeCount,
		UnrecoverableStripeCount: metrics.Stripes.UnrecoverableStripeCount,
		CorruptedStripeCount:     metrics.Stripes.CorruptedStripeCount,
		MissingChunkCount:        metrics.Stripes.MissingChunkCount,
		OrphanChunkCount:         metrics.Stripes.OrphanChunkCount,
		IntegrityErrorCount:      metrics.IntegrityErrorCount,
		RecoveryFailureCount:     metrics.RecoveryFailureCount,
	}
	if phase == LifecycleFailed {
		result.Level = StatusFailed
	} else if phase != LifecycleReady || result.UnrecoverableStripeCount > 0 || result.CorruptedStripeCount > 0 || result.OrphanChunkCount > 0 || result.IntegrityErrorCount > 0 || result.RecoveryFailureCount > 0 {
		result.Level = StatusDegraded
	}
	return result
}

// GetStatus provides a compact health summary. It deliberately avoids per-drive
// device and capacity probes; those belong to GetDetailedStatus.
func (layer *Management) GetStatus(request GetStatusRequest) (GetStatusResponse, error) {
	if err := validateMonitoringContext(request.Context); err != nil {
		return GetStatusResponse{}, err
	}
	if layer.distribution == nil {
		return statusFromMetrics(layer.Phase(), distribution.GetMetricsResponse{}), nil
	}
	metrics, err := layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context, SummaryOnly: true})
	if err != nil {
		return GetStatusResponse{}, fmt.Errorf("get Distribution status: %w", err)
	}
	return statusFromMetrics(layer.Phase(), metrics), nil
}

// GetDetailedStatus gathers a current snapshot from each layer. A failure to
// observe one drive is represented on that drive and does not hide other drives.
func (layer *Management) GetDetailedStatus(request GetDetailedStatusRequest) (GetDetailedStatusResponse, error) {
	if err := validateMonitoringContext(request.Context); err != nil {
		return GetDetailedStatusResponse{}, err
	}
	result := GetDetailedStatusResponse{CollectedAt: time.Now(), Drives: make([]DriveMonitoringStatus, 0, len(layer.driveOrder))}
	if layer.distribution != nil {
		var err error
		result.Distribution, err = layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context})
		if err != nil {
			return GetDetailedStatusResponse{}, fmt.Errorf("get Distribution metrics: %w", err)
		}
	}
	result.Status = statusFromMetrics(layer.Phase(), result.Distribution)
	layer.mu.Lock()
	orchestrationLayer := layer.orchestration
	if orchestrationLayer != nil {
		result.Namespace, err = orchestrationLayer.InspectNamespace(orchestration.InspectNamespaceRequest{Context: request.Context})
		if err != nil {
			layer.mu.Unlock()
			return InspectResponse{}, fmt.Errorf("inspect Orchestration namespace: %w", err)
		}
		result.NamespaceAvailable = true
	} else {
		result.NamespaceError = "Orchestration layer is not initialized"
	}
	layer.mu.Unlock()
	for index, wwn := range layer.driveOrder {
		if err := request.Context.Err(); err != nil {
			return GetDetailedStatusResponse{}, err
		}
		config := layer.drives[wwn]
		drive := DriveMonitoringStatus{WWN: wwn, Root: config.Root, Mountpoint: config.Mountpoint}
		device, deviceErr := platform.ResolveDrive(platform.DriveRequest{Context: request.Context, WWN: wwn})
		if deviceErr != nil {
			drive.DeviceIdentityError = deviceErr.Error()
		} else {
			drive.DeviceIdentityAvailable = true
			drive.DevicePath = device.Path
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
	return result, nil
}

// Inspect performs the detailed monitoring collection and includes the
// Distribution-owned physical inventory. It does not wake standby drives.
func (layer *Management) Inspect(request InspectRequest) (InspectResponse, error) {
	if err := validateMonitoringContext(request.Context); err != nil {
		return InspectResponse{}, err
	}
	result := InspectResponse{CollectedAt: time.Now(), Drives: make([]DriveMonitoringStatus, 0, len(layer.driveOrder))}
	if layer.distribution != nil {
		var err error
		result.Distribution, err = layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context, IncludeDetails: true})
		if err != nil {
			return InspectResponse{}, fmt.Errorf("inspect Distribution: %w", err)
		}
	}
	result.Status = statusFromMetrics(layer.Phase(), result.Distribution)
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
		for _, orphan := range result.Distribution.OrphanChunks {
			if orphan.DriveIndex == index {
				drive.OrphanChunkCount++
			}
		}
		device, deviceErr := platform.ResolveDrive(platform.DriveRequest{Context: request.Context, WWN: wwn})
		if deviceErr != nil {
			drive.DeviceIdentityError = deviceErr.Error()
		} else {
			drive.DeviceIdentityAvailable = true
			drive.DevicePath = device.Path
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
	return result, nil
}
