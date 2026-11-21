package io

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tetsup/flexraid/platform"
)

var temperaturePattern = regexp.MustCompile(`Current Temperature:\s*([0-9]+(?:\.[0-9]+)?)\s*Celsius`)

func probeDevice(ctx context.Context, config DeviceConfig) (DeviceStatus, error) {
	resolved, err := platform.ResolveDrive(platform.DriveRequest{Context: ctx, WWN: config.WWN})
	if err != nil {
		return DeviceStatus{}, err
	}
	status := DeviceStatus{WWN: config.WWN, Mountpoint: config.Mountpoint, DevicePath: resolved.Path}
	status.Model = unavailableValue[string]("sysfs", "model is not exposed by this device")
	status.MediaType = unavailableValue[string]("sysfs", "media type is not exposed by this device")
	status.HasSpindle = unavailableValue[bool]("sysfs", "spindle information is unavailable")
	status.SMARTHealth = DeviceValue[string]{State: ObservationUnknown, Source: "smartctl"}
	status.TemperatureC = DeviceValue[float64]{State: ObservationUnknown, Source: "smartctl"}
	status.SpinState = unavailableValue[string]("sysfs", "spin state is not exposed without device access")
	status.PowerState = unavailableValue[string]("sysfs", "power state is not exposed without device access")

	blockName := filepath.Base(resolved.Path)
	modelPath := filepath.Join("/sys/class/block", blockName, "device", "model")
	if raw, readErr := os.ReadFile(modelPath); readErr == nil {
		status.Model = observedValue(strings.TrimSpace(string(raw)), "sysfs")
	} else {
		status.Model = unavailableValue[string]("sysfs", readErr.Error())
	}
	rotationalPath := filepath.Join("/sys/class/block", blockName, "queue", "rotational")
	if raw, readErr := os.ReadFile(rotationalPath); readErr == nil {
		rotational := strings.TrimSpace(string(raw)) == "1"
		media := "ssd"
		if rotational {
			media = "rotational"
		}
		status.MediaType = observedValue(media, "sysfs")
		status.HasSpindle = observedValue(rotational, "sysfs")
	} else {
		status.MediaType = unavailableValue[string]("sysfs", readErr.Error())
		status.HasSpindle = unavailableValue[bool]("sysfs", readErr.Error())
	}

	if _, lookErr := exec.LookPath("smartctl"); lookErr != nil {
		status.SMARTHealth = DeviceValue[string]{State: ObservationUnavailable, Source: "smartctl", Error: lookErr.Error()}
		status.TemperatureC = DeviceValue[float64]{State: ObservationUnavailable, Source: "smartctl", Error: lookErr.Error()}
	} else {
		// Device-level SMART support is discovered on the first active-I/O window.
		// Initialization only checks tool availability and static sysfs capabilities.
		status.Capabilities.SMARTHealth = true
		status.Capabilities.Temperature = true
	}
	return status, nil
}

func refreshDevicePassive(ctx context.Context, config DeviceConfig, status *DeviceStatus) {
	// No passive query is issued here: unsupported OS interfaces must not cause
	// a sleeping drive to wake merely because status was requested.
}

func refreshDeviceActive(ctx context.Context, config DeviceConfig, status *DeviceStatus, now time.Time, interval time.Duration) {
	if status.Capabilities.SMARTHealth && due(status.SMARTHealth.UpdatedAt, now, interval) {
		output, err := exec.CommandContext(ctx, "smartctl", "-H", status.DevicePath).CombinedOutput()
		text := string(output)
		switch {
		case strings.Contains(text, "PASSED"):
			status.SMARTHealth = DeviceValue[string]{Value: "passed", State: ObservationSupported, Source: "smartctl -H", UpdatedAt: now}
		case strings.Contains(text, "FAILED"):
			status.SMARTHealth = DeviceValue[string]{Value: "failed", State: ObservationSupported, Source: "smartctl -H", UpdatedAt: now}
		case err != nil:
			status.SMARTHealth = DeviceValue[string]{State: ObservationUnavailable, Source: "smartctl -H", UpdatedAt: now, Error: fmt.Sprintf("%v: %s", err, strings.TrimSpace(text))}
		default:
			status.SMARTHealth = DeviceValue[string]{State: ObservationUnsupported, Source: "smartctl -H", UpdatedAt: now}
		}
	}
	if status.Capabilities.Temperature && due(status.TemperatureC.UpdatedAt, now, interval) {
		output, err := exec.CommandContext(ctx, "smartctl", "-l", "scttempsts", status.DevicePath).CombinedOutput()
		if err != nil {
			status.TemperatureC = DeviceValue[float64]{State: ObservationUnavailable, Source: "smartctl -l scttempsts", UpdatedAt: now, Error: fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(output)))}
		} else if match := temperaturePattern.FindStringSubmatch(string(output)); len(match) == 2 {
			value, parseErr := strconv.ParseFloat(match[1], 64)
			if parseErr != nil {
				status.TemperatureC = DeviceValue[float64]{State: ObservationUnavailable, Source: "smartctl -l scttempsts", UpdatedAt: now, Error: parseErr.Error()}
			} else {
				status.TemperatureC = DeviceValue[float64]{Value: value, State: ObservationSupported, Source: "smartctl -l scttempsts", UpdatedAt: now}
			}
		} else {
			status.TemperatureC = DeviceValue[float64]{State: ObservationUnsupported, Source: "smartctl -l scttempsts", UpdatedAt: now}
		}
	}
}

func due(updatedAt, now time.Time, interval time.Duration) bool {
	return updatedAt.IsZero() || now.Sub(updatedAt) >= interval
}

func observedValue[T any](value T, source string) DeviceValue[T] {
	return DeviceValue[T]{Value: value, State: ObservationSupported, Source: source, UpdatedAt: time.Now()}
}

func unavailableValue[T any](source, reason string) DeviceValue[T] {
	return DeviceValue[T]{State: ObservationUnavailable, Source: source, Error: reason}
}
