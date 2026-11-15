package distribution

import (
	"context"
	"errors"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
)

func TestMaxStripeCount(t *testing.T) {
	tests := []struct {
		name      string
		available []uint64
		devices   int
		want      uint64
	}{
		{name: "mirror across uneven drives", available: []uint64{1, 100, 100}, devices: 2, want: 100},
		{name: "parity with spare drive", available: []uint64{10, 10, 10, 10, 10}, devices: 4, want: 12},
		{name: "not enough devices", available: []uint64{100, 100, 100}, devices: 4, want: 0},
		{name: "zero capacity", available: []uint64{0, 10}, devices: 2, want: 0},
		{name: "one device per stripe", available: []uint64{2, 8, 4}, devices: 1, want: 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxStripeCount(tt.available, tt.devices); got != tt.want {
				t.Fatalf("maxStripeCount(%v, %d) = %d, want %d", tt.available, tt.devices, got, tt.want)
			}
		})
	}
}

func TestGetCapacityUsesPlatformRoots(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()}
	ios := make([]*physicalio.IO, len(roots))
	for index, root := range roots {
		ios[index] = physicalio.New(physicalio.NewRequest{Root: root}).Layer
	}
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	got, err := created.Layer.GetCapacity(CapacityRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Capacities) != len(roots) {
		t.Fatalf("got %d capacities, want %d", len(got.Capacities), len(roots))
	}
	for index, capacity := range got.Capacities {
		if capacity.TotalBytes == 0 || capacity.AvailableBytes == 0 {
			t.Errorf("capacity %d is empty: %+v", index, capacity)
		}
	}
}

func TestGetCapacityHonorsCancellation(t *testing.T) {
	roots := []string{t.TempDir()}
	ios := []*physicalio.IO{physicalio.New(physicalio.NewRequest{Root: roots[0]}).Layer}
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := created.Layer.GetCapacity(CapacityRequest{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetCapacity error = %v, want context.Canceled", err)
	}
}
