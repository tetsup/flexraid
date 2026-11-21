package io

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const deviceRefreshInterval = time.Minute

// DeviceConfig binds one I/O root to its physical drive.
type DeviceConfig struct {
	Mountpoint string
	WWN        string
}

// ObservationState distinguishes unknown, unsupported, unavailable, and observed values.
type ObservationState string

const (
	ObservationUnknown     ObservationState = "unknown"
	ObservationSupported   ObservationState = "supported"
	ObservationUnsupported ObservationState = "unsupported"
	ObservationUnavailable ObservationState = "unavailable"
)

// DeviceValue carries one observation together with its provenance and freshness.
type DeviceValue[T any] struct {
	Value     T
	State     ObservationState
	Source    string
	UpdatedAt time.Time
	Error     string
}

// DeviceCapabilities describes which observations can be collected.
type DeviceCapabilities struct {
	SMARTHealth bool
	Temperature bool
	SpinState   bool
	PowerState  bool
}

// DeviceStatus is an independent snapshot of one physical drive's observations.
type DeviceStatus struct {
	WWN          string
	Mountpoint   string
	DevicePath   string
	Model        DeviceValue[string]
	MediaType    DeviceValue[string]
	HasSpindle   DeviceValue[bool]
	SMARTHealth  DeviceValue[string]
	TemperatureC DeviceValue[float64]
	SpinState    DeviceValue[string]
	PowerState   DeviceValue[string]
	Capabilities DeviceCapabilities
}

type deviceState struct {
	mu     sync.RWMutex
	config DeviceConfig
	status DeviceStatus
}

func (layer *IO) InitializeDevice(request InitializeDeviceRequest) (InitializeDeviceResponse, error) {
	if request.Context == nil {
		return InitializeDeviceResponse{}, errors.New("device initialization context is required")
	}
	if err := request.Context.Err(); err != nil {
		return InitializeDeviceResponse{}, err
	}
	if request.Config.WWN == "" || request.Config.Mountpoint == "" {
		return InitializeDeviceResponse{}, errors.New("device WWN and mountpoint are required")
	}
	status, err := probeDevice(request.Context, request.Config)
	if err != nil {
		return InitializeDeviceResponse{}, fmt.Errorf("initialize physical device %q: %w", request.Config.WWN, err)
	}
	layer.device = &deviceState{config: request.Config, status: status}
	return InitializeDeviceResponse{Status: cloneDeviceStatus(status)}, nil
}

// InitializeDeviceRequest configures and probes the physical drive owned by this I/O layer.
type InitializeDeviceRequest struct {
	Context context.Context
	Config  DeviceConfig
}

// InitializeDeviceResponse contains the initial capability snapshot.
type InitializeDeviceResponse struct {
	Status DeviceStatus
}

// StartMonitoring starts event-driven refreshes for values requiring an active drive.
func (layer *IO) StartMonitoring() error {
	if layer.device == nil {
		return errors.New("physical device is not initialized")
	}
	layer.monitorMu.Lock()
	defer layer.monitorMu.Unlock()
	if layer.monitorStop != nil {
		return nil
	}
	layer.monitorWake = make(chan struct{}, 1)
	layer.monitorStop = make(chan struct{})
	layer.monitorDone = make(chan struct{})
	monitorCtx, cancel := context.WithCancel(context.Background())
	layer.monitorCancel = cancel
	go layer.monitorDevice(monitorCtx, layer.monitorWake, layer.monitorStop, layer.monitorDone)
	return nil
}

// StopMonitoring stops the device observation worker and waits for it to exit.
func (layer *IO) StopMonitoring(ctx context.Context) error {
	if ctx == nil {
		return errors.New("monitor stop context is required")
	}
	layer.monitorMu.Lock()
	stop, done, cancel := layer.monitorStop, layer.monitorDone, layer.monitorCancel
	if stop == nil {
		layer.monitorMu.Unlock()
		return nil
	}
	close(stop)
	layer.monitorStop = nil
	layer.monitorCancel = nil
	cancel()
	layer.monitorMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetDeviceStatus refreshes non-invasive observations and returns a detached snapshot.
func (layer *IO) GetDeviceStatus(request GetDeviceStatusRequest) (GetDeviceStatusResponse, error) {
	if request.Context == nil {
		return GetDeviceStatusResponse{}, errors.New("device status context is required")
	}
	if err := request.Context.Err(); err != nil {
		return GetDeviceStatusResponse{}, err
	}
	if layer.device == nil {
		return GetDeviceStatusResponse{}, errors.New("physical device is not initialized")
	}
	layer.device.mu.Lock()
	refreshDevicePassive(request.Context, layer.device.config, &layer.device.status)
	status := cloneDeviceStatus(layer.device.status)
	layer.device.mu.Unlock()
	return GetDeviceStatusResponse{Status: status}, nil
}

// GetDeviceStatusRequest requests the current physical device observations.
type GetDeviceStatusRequest struct {
	Context context.Context
}

// GetDeviceStatusResponse contains an independent physical device snapshot.
type GetDeviceStatusResponse struct {
	Status DeviceStatus
}

func (layer *IO) monitorDevice(ctx context.Context, wake <-chan struct{}, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case <-wake:
			if layer.device == nil {
				continue
			}
			layer.device.mu.Lock()
			refreshDeviceActive(ctx, layer.device.config, &layer.device.status, time.Now(), deviceRefreshInterval)
			layer.device.mu.Unlock()
		}
	}
}

func cloneDeviceStatus(status DeviceStatus) DeviceStatus {
	return status
}
