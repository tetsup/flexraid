package io

import (
	"os"
	"testing"
)

func TestGetCapacityReportsFilesystemCapacity(t *testing.T) {
	layer := New(NewRequest{Root: t.TempDir()}).Layer
	result, err := layer.GetCapacity(CapacityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Capacity.TotalBytes == 0 {
		t.Fatal("total capacity is zero")
	}
	if result.Capacity.FreeBytes == 0 {
		t.Fatal("free capacity is zero")
	}
	if result.Capacity.AvailableBytes == 0 {
		t.Fatal("available capacity is zero")
	}
}

func TestGetCapacityReturnsErrorWhenIORootIsUnavailable(t *testing.T) {
	root := t.TempDir()
	layer := New(NewRequest{Root: root}).Layer
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err := layer.GetCapacity(CapacityRequest{}); err == nil {
		t.Fatal("GetCapacity succeeded although the I/O root is unavailable")
	}
}
