package main

import (
	"math"
	"strings"
	"testing"
)

func TestRenderConfigSelectsGRPCMode(t *testing.T) {
	template := "disks:\n" +
		"  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: w1\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: w2\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: w4\n" +
		"mountpoint: /mnt/flexraid\n" +
		"grpc_socket_path: /run/flexraid/worker.sock\n"
	roots := []string{"/tmp/d1", "/tmp/d2", "/tmp/d3", "/tmp/d4"}
	for _, test := range []struct {
		name    string
		enabled bool
		want    string
	}{
		{name: "enabled", enabled: true, want: "grpc_enabled: true"},
		{name: "disabled", enabled: false, want: "grpc_enabled: false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := renderConfig(template, roots, "/tmp/flexraid-mount", "/tmp/flexraid.sock", test.enabled)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, test.want) {
				t.Fatalf("generated config does not contain %q:\n%s", test.want, got)
			}
			if !strings.Contains(got, "grpc_socket_path: /tmp/flexraid.sock") {
				t.Fatalf("generated config does not contain isolated socket path:\n%s", got)
			}
			if !strings.Contains(got, "root: /tmp/d1") || !strings.Contains(got, "mountpoint: /tmp/flexraid-mount") {
				t.Fatalf("generated config did not replace test paths:\n%s", got)
			}
		})
	}
}

func TestPercentage(t *testing.T) {
	if got := percentage(100, 95); math.Abs(got+5) > 1e-9 {
		t.Fatalf("percentage(100, 95) = %v, want -5", got)
	}
	if got := percentage(0, 95); got != 0 {
		t.Fatalf("percentage(0, 95) = %v, want 0", got)
	}
}

func TestSummarizeResultsUsesMedian(t *testing.T) {
	samples := []result{
		{Mode: modeOff, WriteMiBPerSec: 100, ReadMiBPerSec: 200},
		{Mode: modeOff, WriteMiBPerSec: 300, ReadMiBPerSec: 400},
		{Mode: modeOff, WriteMiBPerSec: 200, ReadMiBPerSec: 300},
		{Mode: modeIdle, WriteMiBPerSec: 90, ReadMiBPerSec: 180},
	}
	got := summarizeResults(samples)
	if len(got) != 2 {
		t.Fatalf("summary length = %d, want 2", len(got))
	}
	if got[0].Mode != modeOff || got[0].WriteMiBPerSec != 200 || got[0].ReadMiBPerSec != 300 {
		t.Fatalf("off median = %+v", got[0])
	}
	if got[1].Mode != modeIdle || got[1].WriteMiBPerSec != 90 {
		t.Fatalf("idle summary = %+v", got[1])
	}
}
