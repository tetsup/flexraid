package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flexraid.yaml")
	yaml := "disks:\n  - /data/disk1\n  - /data/disk2\n  - /data/disk3\n  - /data/disk4\nmountpoint: /mnt/flexraid\n"
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if len(config.DiskRoots) != 4 {
		t.Fatalf("got %d disk roots, want 4", len(config.DiskRoots))
	}
	if config.Mountpoint != "/mnt/flexraid" {
		t.Errorf("mountpoint = %q, want /mnt/flexraid", config.Mountpoint)
	}
}

func TestLoadConfigRejectsInvalidDiskCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flexraid.yaml")
	if err := os.WriteFile(path, []byte("disks:\n  - /data/disk1\nmountpoint: /mnt/flexraid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig() succeeded with fewer than four disks")
	}
}
