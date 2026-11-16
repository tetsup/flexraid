package management

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManagementConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flexraid.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	yaml := "disks:\n" +
		"  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: 0x5000000000000001\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: 0x5000000000000002\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: 0x5000000000000003\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: 0x5000000000000004\n" +
		"mountpoint: /mnt/flexraid\n"
	got, err := LoadConfig(writeManagementConfig(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Disks) != 4 || got.Disks[0].Root != "/data/disk1" ||
		got.Disks[0].Mountpoint != "/mnt/disk1" || got.Disks[0].WWN != "0x5000000000000001" {
		t.Fatalf("unexpected config: %+v", got)
	}
	if got.Mountpoint != "/mnt/flexraid" {
		t.Fatalf("filesystem mountpoint = %q", got.Mountpoint)
	}
}

func TestLoadConfigRejectsMissingWWN(t *testing.T) {
	yaml := "disks:\n" +
		"  - root: /data/disk1\n    mountpoint: /mnt/disk1\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: w2\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: w4\n" +
		"mountpoint: /mnt/flexraid\n"
	if _, err := LoadConfig(writeManagementConfig(t, yaml)); err == nil ||
		!strings.Contains(err.Error(), "requires root, mountpoint, and WWN") {
		t.Fatalf("LoadConfig error = %v, want missing WWN error", err)
	}
}

func TestLoadConfigRejectsDuplicateWWN(t *testing.T) {
	yaml := "disks:\n" +
		"  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: same\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: same\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: w4\n" +
		"mountpoint: /mnt/flexraid\n"
	if _, err := LoadConfig(writeManagementConfig(t, yaml)); err == nil {
		t.Fatal("LoadConfig succeeded with duplicate WWN")
	}
}

func TestLoadConfigRejectsFilesystemMountpointCollision(t *testing.T) {
	yaml := "disks:\n" +
		"  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: w1\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: w2\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: w4\n" +
		"mountpoint: /mnt/disk1\n"
	if _, err := LoadConfig(writeManagementConfig(t, yaml)); err == nil {
		t.Fatal("LoadConfig succeeded with colliding mountpoints")
	}
}
