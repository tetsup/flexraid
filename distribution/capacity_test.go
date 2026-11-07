package distribution

import (
	"os"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
)

func TestCapacityReturnsEachIOCapacityInIndexOrder(t *testing.T) {
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

func TestCapacityReturnsErrorWhenAnyIOFails(t *testing.T) {
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
