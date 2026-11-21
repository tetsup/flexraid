package management

import (
	"context"
	"fmt"
	"time"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
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
	Status       GetStatusResponse
	Distribution distribution.GetMetricsResponse
	Drives       []DriveMonitoringStatus
	CollectedAt  time.Time
}

func validateMonitoringContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return ctx.Err()
}

// GetStatus provides a compact health summary. It deliberately avoids per-drive
// device and capacity probes; those belong to GetDetailedStatus.
func (layer *Management) GetStatus(request GetStatusRequest) (GetStatusResponse, error) {
	if err := validateMonitoringContext(request.Context); err != nil {
		return GetStatusResponse{}, err
	}
	result := GetStatusResponse{Lifecycle: layer.Phase(), Level: StatusHealthy}
	if result.Lifecycle == LifecycleFailed {
		result.Level = StatusFailed
	} else if result.Lifecycle != LifecycleReady {
		result.Level = StatusDegraded
	}
	if layer.distribution == nil {
		result.Level = StatusFailed
		return result, nil
	}
	metrics, err := layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context})
	if err != nil {
		return GetStatusResponse{}, fmt.Errorf("get Distribution status: %w", err)
	}
	result.ObjectCount = metrics.Stripes.ObjectCount
	result.StripeCount = metrics.Stripes.StripeCount
	result.UnrecoverableStripeCount = metrics.Stripes.UnrecoverableStripeCount
	result.CorruptedStripeCount = metrics.Stripes.CorruptedStripeCount
	result.MissingChunkCount = metrics.Stripes.MissingChunkCount
	result.OrphanChunkCount = metrics.Stripes.OrphanChunkCount
	result.IntegrityErrorCount = metrics.IntegrityErrorCount
	result.RecoveryFailureCount = metrics.RecoveryFailureCount
	if result.Level != StatusFailed && (result.UnrecoverableStripeCount > 0 || result.CorruptedStripeCount > 0 || result.OrphanChunkCount > 0 || result.IntegrityErrorCount > 0 || result.RecoveryFailureCount > 0) {
		result.Level = StatusDegraded
	}
	return result, nil
}

// GetDetailedStatus gathers a current snapshot from each layer. A failure to
// observe one drive is represented on that drive and does not hide other drives.
func (layer *Management) GetDetailedStatus(request GetDetailedStatusRequest) (GetDetailedStatusResponse, error) {
	if err := validateMonitoringContext(request.Context); err != nil {
		return GetDetailedStatusResponse{}, err
	}
	status, err := layer.GetStatus(GetStatusRequest{Context: request.Context})
	if err != nil {
		return GetDetailedStatusResponse{}, err
	}
	result := GetDetailedStatusResponse{Status: status, CollectedAt: time.Now(), Drives: make([]DriveMonitoringStatus, 0, len(layer.driveOrder))}
	if layer.distribution != nil {
		result.Distribution, err = layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context})
		if err != nil {
			return GetDetailedStatusResponse{}, fmt.Errorf("get Distribution metrics: %w", err)
		}
	}
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
	status, err := layer.GetStatus(GetStatusRequest{Context: request.Context})
	if err != nil {
		return InspectResponse{}, err
	}
	result := InspectResponse{Status: status, CollectedAt: time.Now(), Drives: make([]DriveMonitoringStatus, 0, len(layer.driveOrder))}
	if layer.distribution != nil {
		result.Distribution, err = layer.distribution.GetMetrics(distribution.GetMetricsRequest{Context: request.Context, IncludeDetails: true})
		if err != nil {
			return InspectResponse{}, fmt.Errorf("inspect Distribution: %w", err)
		}
	}
	for index, wwn := range layer.driveOrder {
		if err := request.Context.Err(); err != nil {
			return InspectResponse{}, err
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
		result.Drives = append(result.Drives, drive)
	}
	return result, nil
}
