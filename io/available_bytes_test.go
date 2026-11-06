package io

import "testing"

func TestAvailableBytesReportsFilesystemCapacity(t *testing.T) {
	layer := New(NewRequest{Root: t.TempDir()}).Layer
	available, err := layer.AvailableBytes(AvailableBytesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if available.Bytes == 0 {
		t.Fatal("available capacity is zero")
	}
}
