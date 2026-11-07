package io

import (
	"os"
	"testing"
)

func TestCapacityReportsFilesystemCapacity(t *testing.T) {
	layer := New(NewRequest{Root: t.TempDir()}).Layer
	result, err := layer.Capacity(CapacityRequest{})
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

func TestCapacityReturnsErrorWhenIORootIsUnavailable(t *testing.T) {
	root := t.TempDir()
	layer := New(NewRequest{Root: root}).Layer
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err := layer.Capacity(CapacityRequest{}); err == nil {
		t.Fatal("Capacity succeeded although the I/O root is unavailable")
	}
}
