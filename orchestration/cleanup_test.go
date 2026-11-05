package orchestration

import (
	"context"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestStartupRebuildsDeletionQueueAndCleanupDeletesObject(t *testing.T) {
	ctx := context.Background()
	ios := []*physicalio.IO{
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
	}
	initial, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	deletedID := testID("22345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	deleted := testHeader(objectKindFile, deletedID, &rootID, nil, "deleted")
	deleted.Deleted = true
	headerData, err := deleted.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	rootData, err := root.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id     shared.ObjectID
		stripe storage.StripeID
		data   []byte
	}{{rootID, 0, rootData}, {deletedID, 0, headerData}, {deletedID, 1, []byte("payload")}} {
		if _, err := initial.Layer.WriteStripe(distribution.WriteStripeRequest{
			Context: ctx, ObjectID: item.id, StripeID: item.stripe, ChunkSize: 4096,
			Layout: distribution.Layout{Type: distribution.LayoutMirror, NumChunks: 2},
			IOIndexes: []int{0, 1}, Data: item.data,
		}); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	started, err := New(NewRequest{Context: ctx, Distribution: restarted.Layer})
	if err != nil {
		t.Fatal(err)
	}
	if len(started.Layer.deletionQueue) != 1 {
		t.Fatalf("deletion queue has %d entries", len(started.Layer.deletionQueue))
	}
	cleaned, err := started.Layer.Cleanup(CleanupRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.DeletedObjects != 1 {
		t.Fatalf("deleted objects=%d", cleaned.DeletedObjects)
	}
	objects, err := restarted.Layer.ListObjects(distribution.ListObjectsRequest{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.ObjectIDs) != 1 || objects.ObjectIDs[0] != rootID {
		t.Fatalf("unexpected remaining objects: %v", objects.ObjectIDs)
	}
}
