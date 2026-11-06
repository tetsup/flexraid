package distribution

import (
	"reflect"
	"testing"

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
