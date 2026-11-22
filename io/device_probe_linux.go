package io

import (
	"context"
	"encoding/json"
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

var (
	temperaturePattern = regexp.MustCompile(`Current Temperature:\s*([0-9]+(?:\.[0-9]+)?)\s*Celsius`)
	healthPattern      = regexp.MustCompile(`(?i)(?:SMART overall-health self-assessment test result|SMART Health Status):\s*(\S+)`)
)

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
	status.SpinState = DeviceValue[string]{State: ObservationUnknown, Source: "hdparm"}
	status.PowerState = DeviceValue[string]{State: ObservationUnknown, Source: "hdparm"}

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

	smartctl, lookErr := exec.LookPath("smartctl")
	if lookErr != nil {
		status.SMARTHealth = unavailableValue[string]("smartctl", lookErr.Error())
		status.TemperatureC = unavailableValue[float64]("smartctl", lookErr.Error())
	} else {
		status.Capabilities.SMARTHealthProbeAvailable = true
		status.Capabilities.TemperatureProbeAvailable = true
		probeSMART(ctx, smartctl, &status, time.Now())
	}
	probePowerState(ctx, &status, time.Now())
	return status, nil
}

// refreshDevicePassive intentionally does not issue device commands. Status
// reads must never wake a disk solely to refresh observations.
func refreshDevicePassive(ctx context.Context, config DeviceConfig, status *DeviceStatus) {}

func refreshDeviceActive(ctx context.Context, config DeviceConfig, status *DeviceStatus, now time.Time, interval time.Duration) {
	if status.Capabilities.SMARTHealthProbeAvailable &&
		(due(status.SMARTHealth.UpdatedAt, now, interval) || due(status.TemperatureC.UpdatedAt, now, interval)) {
		smartctl, err := exec.LookPath("smartctl")
		if err != nil {
			status.SMARTHealth = unavailableValue[string]("smartctl", err.Error())
			status.TemperatureC = unavailableValue[float64]("smartctl", err.Error())
		} else {
			if due(status.SMARTHealth.UpdatedAt, now, interval) {
				probeSMARTHealth(ctx, smartctl, status, now)
			}
			if due(status.TemperatureC.UpdatedAt, now, interval) {
				probeTemperature(ctx, smartctl, status, now)
			}
		}
	}
	if due(status.PowerState.UpdatedAt, now, interval) || due(status.SpinState.UpdatedAt, now, interval) {
		probePowerState(ctx, status, now)
	}
}

func probeSMART(ctx context.Context, smartctl string, status *DeviceStatus, now time.Time) {
	output, err := exec.CommandContext(ctx, smartctl, "-i", status.DevicePath).CombinedOutput()
	text := string(output)
	if err != nil {
		status.SMARTHealth = unavailableValue[string]("smartctl -i", fmt.Sprintf("%v: %s", err, strings.TrimSpace(text)))
		status.TemperatureC = unavailableValue[float64]("smartctl -i", fmt.Sprintf("%v: %s", err, strings.TrimSpace(text)))
		return
	}
	if !strings.Contains(text, "SMART support is: Available") {
		status.SMARTHealth = DeviceValue[string]{State: ObservationUnsupported, Source: "smartctl -i", UpdatedAt: now}
		status.TemperatureC = DeviceValue[float64]{State: ObservationUnsupported, Source: "smartctl -i", UpdatedAt: now}
		status.Capabilities.SMARTHealthProbeAvailable = false
		status.Capabilities.TemperatureProbeAvailable = false
		return
	}
	if !strings.Contains(text, "SMART support is: Enabled") {
		status.SMARTHealth = DeviceValue[string]{State: ObservationUnavailable, Source: "smartctl -i", UpdatedAt: now, Error: "SMART is supported but disabled"}
		status.TemperatureC = DeviceValue[float64]{State: ObservationUnavailable, Source: "smartctl -i", UpdatedAt: now, Error: "SMART is supported but disabled"}
		return
	}
	probeSMARTHealth(ctx, smartctl, status, now)
	probeTemperature(ctx, smartctl, status, now)
}

func probeSMARTHealth(ctx context.Context, smartctl string, status *DeviceStatus, now time.Time) {
	output, err := exec.CommandContext(ctx, smartctl, "-H", status.DevicePath).CombinedOutput()
	text := string(output)
	if match := healthPattern.FindStringSubmatch(text); len(match) == 2 {
		value := strings.ToLower(match[1])
		state := ObservationSupported
		if value != "passed" && value != "ok" {
			value = "failed: " + value
		}
		status.SMARTHealth = DeviceValue[string]{Value: value, State: state, Source: "smartctl -H", UpdatedAt: now}
		return
	}
	if err != nil {
		status.SMARTHealth = DeviceValue[string]{State: ObservationUnavailable, Source: "smartctl -H", UpdatedAt: now, Error: fmt.Sprintf("%v: %s", err, strings.TrimSpace(text))}
	} else {
		status.SMARTHealth = DeviceValue[string]{State: ObservationUnsupported, Source: "smartctl -H", UpdatedAt: now}
	}
}

func probeTemperature(ctx context.Context, smartctl string, status *DeviceStatus, now time.Time) {
	output, err := exec.CommandContext(ctx, smartctl, "-l", "scttempsts", status.DevicePath).CombinedOutput()
	text := string(output)
	if err != nil && strings.HasPrefix(status.DevicePath, "/dev/nvme") {
		if nvme, lookErr := exec.LookPath("nvme"); lookErr == nil {
			nvmeOutput, nvmeErr := exec.CommandContext(ctx, nvme, "smart-log", "--output-format=json", status.DevicePath).CombinedOutput()
			if nvmeErr == nil {
				var log struct { Temperature float64 `json:"temperature"` }
				if jsonErr := json.Unmarshal(nvmeOutput, &log); jsonErr == nil {
					status.TemperatureC = DeviceValue[float64]{Value: log.Temperature, State: ObservationSupported, Source: "nvme smart-log", UpdatedAt: now}
					return
				}
			}
		}
	}
	if err != nil {
		status.TemperatureC = DeviceValue[float64]{State: ObservationUnavailable, Source: "smartctl -l scttempsts", UpdatedAt: now, Error: fmt.Sprintf("%v: %s", err, strings.TrimSpace(text))}
		return
	}
	if match := temperaturePattern.FindStringSubmatch(text); len(match) == 2 {
		value, parseErr := strconv.ParseFloat(match[1], 64)
		if parseErr != nil {
			status.TemperatureC = DeviceValue[float64]{State: ObservationUnavailable, Source: "smartctl -l scttempsts", UpdatedAt: now, Error: parseErr.Error()}
			return
		}
		status.TemperatureC = DeviceValue[float64]{Value: value, State: ObservationSupported, Source: "smartctl -l scttempsts", UpdatedAt: now}
		return
	}
	status.TemperatureC = DeviceValue[float64]{State: ObservationUnsupported, Source: "smartctl -l scttempsts", UpdatedAt: now}
}

func probePowerState(ctx context.Context, status *DeviceStatus, now time.Time) {
	hdparm, err := exec.LookPath("hdparm")
	if err != nil {
		status.PowerState = unavailableValue[string]("hdparm", err.Error())
		if status.HasSpindle.State == ObservationSupported && status.HasSpindle.Value {
			status.SpinState = unavailableValue[string]("hdparm", err.Error())
		} else if status.HasSpindle.State == ObservationSupported {
			status.SpinState = DeviceValue[string]{State: ObservationUnsupported, Source: "hdparm", UpdatedAt: now}
		}
		return
	}
	status.Capabilities.PowerStateProbeAvailable = true
	status.Capabilities.SpinStateProbeAvailable = status.HasSpindle.State == ObservationSupported && status.HasSpindle.Value
	output, err := exec.CommandContext(ctx, hdparm, "-C", status.DevicePath).CombinedOutput()
	text := string(output)
	if err != nil {
		status.PowerState = DeviceValue[string]{State: ObservationUnavailable, Source: "hdparm -C", UpdatedAt: now, Error: fmt.Sprintf("%v: %s", err, strings.TrimSpace(text))}
		if status.HasSpindle.State == ObservationSupported && status.HasSpindle.Value {
			status.SpinState = DeviceValue[string]{State: ObservationUnavailable, Source: "hdparm -C", UpdatedAt: now, Error: status.PowerState.Error}
		}
		return
	}
	state := parsePowerState(text)
	if state == "" {
		status.PowerState = DeviceValue[string]{State: ObservationUnsupported, Source: "hdparm -C", UpdatedAt: now}
		status.Capabilities.PowerStateProbeAvailable = false
		if status.HasSpindle.State == ObservationSupported && status.HasSpindle.Value {
			status.Capabilities.SpinStateProbeAvailable = false
			status.SpinState = DeviceValue[string]{State: ObservationUnsupported, Source: "hdparm -C", UpdatedAt: now}
			status.Capabilities.SpinStateProbeAvailable = false
		}
		return
	}
	status.PowerState = DeviceValue[string]{Value: state, State: ObservationSupported, Source: "hdparm -C", UpdatedAt: now}
	if status.HasSpindle.State == ObservationSupported && status.HasSpindle.Value {
		spin := "spinning"
		if state == "standby" || state == "sleeping" {
			spin = "stopped"
		}
		status.SpinState = DeviceValue[string]{Value: spin, State: ObservationSupported, Source: "hdparm -C", UpdatedAt: now}
	} else if status.HasSpindle.State == ObservationSupported {
		status.SpinState = DeviceValue[string]{State: ObservationUnsupported, Source: "hdparm -C", UpdatedAt: now}
	}
}

func parsePowerState(output string) string {
	lower := strings.ToLower(output)
	for _, state := range []string{"active/idle", "standby", "sleeping", "unknown"} {
		if strings.Contains(lower, state) {
			return strings.ReplaceAll(state, "active/idle", "active")
		}
	}
	return ""
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
