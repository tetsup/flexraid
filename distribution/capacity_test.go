package distribution

import "testing"

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
