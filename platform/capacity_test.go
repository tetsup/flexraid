package platform

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestGetCapacityReportsFilesystemCapacity(t *testing.T) {
	result, err := GetCapacity(CapacityRequest{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Capacity.TotalBytes == 0 || result.Capacity.FreeBytes == 0 || result.Capacity.AvailableBytes == 0 {
		t.Fatalf("unexpected filesystem capacity: %+v", result.Capacity)
	}
}

func TestGetCapacityReturnsErrorForUnavailableRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err := GetCapacity(CapacityRequest{Root: root}); err == nil {
		t.Fatal("GetCapacity succeeded for an unavailable root")
	}
}

func TestGetCapacityHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GetCapacity(CapacityRequest{Context: ctx, Root: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetCapacity error = %v, want context.Canceled", err)
	}
}

func TestGetCapacityRequiresRoot(t *testing.T) {
	if _, err := GetCapacity(CapacityRequest{}); err == nil {
		t.Fatal("GetCapacity succeeded without a root")
	}
}
