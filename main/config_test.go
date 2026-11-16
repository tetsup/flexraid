package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flexraid.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validConfig(disks string) string {
	return "disks:\n" + disks + "mountpoint: /mnt/flexraid\n"
}

func TestLoadConfig(t *testing.T) {
	yaml := validConfig("  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: 0x5000000000000001\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: 0x5000000000000002\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: 0x5000000000000003\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: 0x5000000000000004\n")
	config, err := loadConfig(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if len(config.Disks) != 4 || config.Disks[0].Root != "/data/disk1" ||
		config.Disks[0].Mountpoint != "/mnt/disk1" || config.Disks[0].WWN != "0x5000000000000001" {
		t.Fatalf("unexpected disk configuration: %+v", config.Disks)
	}
	if config.Mountpoint != "/mnt/flexraid" {
		t.Errorf("mountpoint = %q, want /mnt/flexraid", config.Mountpoint)
	}
}

func TestLoadConfigRejectsMissingWWN(t *testing.T) {
	yaml := validConfig("  - root: /data/disk1\n    mountpoint: /mnt/disk1\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: w2\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: w4\n")
	if _, err := loadConfig(writeConfig(t, yaml)); err == nil || !strings.Contains(err.Error(), "requires root, mountpoint, and WWN") {
		t.Fatalf("loadConfig error = %v, want missing WWN error", err)
	}
}

func TestLoadConfigRejectsDuplicateWWN(t *testing.T) {
	yaml := validConfig("  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: same\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: same\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n" +
		"  - root: /data/disk4\n    mountpoint: /mnt/disk4\n    wwn: w4\n")
	if _, err := loadConfig(writeConfig(t, yaml)); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("loadConfig error = %v, want duplicate WWN error", err)
	}
}

func TestLoadConfigRequiresMinimumDiskCount(t *testing.T) {
	yaml := validConfig("  - root: /data/disk1\n    mountpoint: /mnt/disk1\n    wwn: w1\n" +
		"  - root: /data/disk2\n    mountpoint: /mnt/disk2\n    wwn: w2\n" +
		"  - root: /data/disk3\n    mountpoint: /mnt/disk3\n    wwn: w3\n")
	if _, err := loadConfig(writeConfig(t, yaml)); err == nil || !strings.Contains(err.Error(), "at least 4") {
		t.Fatalf("loadConfig error = %v, want minimum disk count error", err)
	}
}
