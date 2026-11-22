package io

import (
	"testing"
	"time"
)

func TestDeviceRefreshDuePerObservation(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if !due(time.Time{}, now, time.Minute) {
		t.Fatal("never-observed value must be due")
	}
	if due(now.Add(-59*time.Second), now, time.Minute) {
		t.Fatal("value refreshed less than one minute ago must not be due")
	}
	if !due(now.Add(-time.Minute), now, time.Minute) {
		t.Fatal("value refreshed one minute ago must be due")
	}
}

func TestRecordAccessNotifiesMonitorWithoutBlocking(t *testing.T) {
	layer := &IO{monitorWake: make(chan struct{}, 1)}
	layer.recordAccess()
	select {
	case <-layer.monitorWake:
	default:
		t.Fatal("physical access did not notify the monitor")
	}

	// A full notification channel is coalesced rather than blocking I/O.
	layer.monitorWake <- struct{}{}
	layer.recordAccess()
	if got := layer.lastAccessTime(); got.IsZero() {
		t.Fatal("physical access timestamp was not updated")
	}
}

func TestDeviceStatusSnapshotIsValueCopy(t *testing.T) {
	original := DeviceStatus{
		WWN: "wwn-test",
		SMARTHealth: DeviceValue[string]{
			Value: "passed", State: ObservationSupported, Source: "test",
		},
	}
	snapshot := cloneDeviceStatus(original)
	snapshot.SMARTHealth.Value = "failed"
	if original.SMARTHealth.Value != "passed" {
		t.Fatal("snapshot mutation changed the original device status")
	}
}

func TestParsePowerState(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "active", text: "drive state is: active/idle", want: "active"},
		{name: "standby", text: "drive state is: standby", want: "standby"},
		{name: "sleeping", text: "drive state is: sleeping", want: "sleeping"},
		{name: "unknown", text: "drive state is: unknown", want: "unknown"},
		{name: "unrecognized", text: "no state reported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parsePowerState(test.text); got != test.want {
				t.Fatalf("parsePowerState() = %q, want %q", got, test.want)
			}
		})
	}
}
