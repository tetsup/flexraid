package distribution

import (
	"os"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
)

func TestGetCapacityReturnsEachIOCapacityInIndexOrder(t *testing.T) {
	first := physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	second := physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	d := &Distribution{ios: []*physicalio.IO{first, second}}

	got, err := d.GetCapacity(CapacityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Capacities) != 2 {
		t.Fatalf("capacity count = %d, want 2", len(got.Capacities))
	}
	for index, capacity := range got.Capacities {
		if capacity.TotalBytes == 0 || capacity.FreeBytes == 0 || capacity.AvailableBytes == 0 {
			t.Fatalf("capacity[%d] = %+v, want all capacity fields", index, capacity)
		}
	}
}

func TestGetCapacityReturnsErrorWhenAnyIOFails(t *testing.T) {
	root := t.TempDir()
	layer := physicalio.New(physicalio.NewRequest{Root: root}).Layer
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	d := &Distribution{ios: []*physicalio.IO{layer}}

	if _, err := d.GetCapacity(CapacityRequest{}); err == nil {
		t.Fatal("GetCapacity succeeded although an I/O root is unavailable")
	}
}

func TestMaxStripeCount(t *testing.T) {
	tests := []struct {
		name      string
		available []uint64
		devices   int
		want      uint64
	}{
		{name: "mirror across uneven drives", available: []uint64{1, 100, 100}, devices: 2, want: 99},
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
