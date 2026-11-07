package distribution

import (
	"os"
	"reflect"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestStripeIOIndexesReturnsDataOrderThenParity(t *testing.T) {
	objectID, err := shared.NewObjectID()
	if err != nil {
		t.Fatal(err)
	}
	d := &Distribution{
		objects: map[shared.ObjectID]map[storage.StripeID]*stripeInfo{
			objectID: {
				0: {
					method: distributionMethod{distributionType: distributionParity, numChunks: 3, numParities: 1},
					chunks: map[chunkNo]chunkLocation{
						0: {ioIndex: 2}, 1: {ioIndex: 0}, 2: {ioIndex: 3}, parityFlag: {ioIndex: 1},
					},
				},
			},
		},
	}
	got, err := d.StripeIOIndexes(StripeIOIndexesRequest{ObjectID: objectID, StripeID: 0})
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 0, 3, 1}
	if !reflect.DeepEqual(got.IOIndexes, want) {
		t.Fatalf("indexes = %v, want %v", got.IOIndexes, want)
	}
}


func TestAvailableBytesReturnsEachIOCapacityInIndexOrder(t *testing.T) {
	first := physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	second := physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	d := &Distribution{ios: []*physicalio.IO{first, second}}

	got, err := d.AvailableBytes(AvailableBytesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bytes) != 2 {
		t.Fatalf("capacity count = %d, want 2", len(got.Bytes))
	}
	for index, capacity := range got.Bytes {
		if capacity == 0 {
			t.Fatalf("capacity[%d] = 0, want available bytes", index)
		}
	}
}

func TestAvailableBytesReturnsErrorWhenAnyIOFails(t *testing.T) {
	root := t.TempDir()
	layer := physicalio.New(physicalio.NewRequest{Root: root}).Layer
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	d := &Distribution{ios: []*physicalio.IO{layer}}

	if _, err := d.AvailableBytes(AvailableBytesRequest{}); err == nil {
		t.Fatal("AvailableBytes succeeded although an I/O root is unavailable")
	}
}
