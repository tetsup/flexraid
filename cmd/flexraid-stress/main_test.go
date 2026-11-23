package main

import (
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
	if got := percentage(100, 95); got != -5 {
		t.Fatalf("percentage(100, 95) = %v, want -5", got)
	}
	if got := percentage(0, 95); got != 0 {
		t.Fatalf("percentage(0, 95) = %v, want 0", got)
	}
}
