package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadConfigAcceptsAdditionalDisks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flexraid.yaml")
	yaml := "disks:\n  - /data/disk1\n  - /data/disk2\n  - /data/disk3\n  - /data/disk4\n  - /data/disk5\n  - /data/disk6\nmountpoint: /mnt/flexraid\n"
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if len(config.DiskRoots) != 6 {
		t.Fatalf("got %d disk roots, want 6", len(config.DiskRoots))
	}
}

func TestLoadConfigRejectsFewerThanMinimumDisks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flexraid.yaml")
	yaml := "disks:\n  - /data/disk1\n  - /data/disk2\n  - /data/disk3\nmountpoint: /mnt/flexraid\n"
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig() succeeded with fewer than four disks")
	} else if !strings.Contains(err.Error(), "at least 4") {
		t.Fatalf("error = %q, want minimum disk count message", err)
	}
}
