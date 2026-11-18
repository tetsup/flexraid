package distribution

import (
	"context"
	"errors"
	"os"
	"testing"

	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/storage"
)

func TestCleanupOneDeletesOnlyOneOldChunkOnRequestedDrive(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	request := WriteStripeRequest{
		Context: ctx, ObjectID: id, StripeID: 8, ChunkSize: 16,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
		Data: []byte("first"),
	}
	first, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("second")
	if _, err := created.Layer.WriteStripe(request); err != nil {
		t.Fatal(err)
	}

	result, err := created.Layer.CleanupOne(CleanupOneRequest{
		Context: ctx, IOIndex: 0, Method: CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Processed || result.ObjectID != id || result.StripeID != request.StripeID || result.Key.StripeVersion != first.Version {
		t.Fatalf("CleanupOne result = %+v", result)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: result.Key}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted chunk still exists on requested drive: %v", err)
	}
	otherKey := result.Key
	otherKey.LayoutID.Index = 2
	if _, err := ios[1].Read(physicalio.ReadRequest{Context: ctx, Key: otherKey}); err != nil {
		t.Fatalf("chunk on another drive was touched: %v", err)
	}
	if got := created.Layer.cleanupCandidates(); len(got) != 1 || got[0].ioIndex != 1 {
		t.Fatalf("remaining candidates = %+v, want only drive 1", got)
	}
}

func TestCleanupOnePreservesAbnormalChunk(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	id := testObjectID()
	request := WriteStripeRequest{
		Context: ctx, ObjectID: id, StripeID: 9, ChunkSize: 16,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
		Data: []byte("current"),
	}
	written, err := created.Layer.WriteStripe(request)
	if err != nil {
		t.Fatal(err)
	}
	abnormalKey := storage.FileKey{
		ObjectID: id, StripeID: request.StripeID, StripeVersion: written.Version + 1,
		LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: 1},
	}
	if _, err := ios[0].Write(physicalio.WriteRequest{Context: ctx, Key: abnormalKey, Data: encodeChunk(7, []byte("orphan"))}); err != nil {
		t.Fatal(err)
	}
	layer := created.Layer
	layer.mapMu.Lock()
	layer.orphans[id] = append(layer.orphans[id], chunkLocation{ioIndex: 0, key: abnormalKey})
	layer.refreshStripeCleanupQueueLocked(stripeIDKey{objectID: id, stripeID: request.StripeID})
	layer.mapMu.Unlock()

	result, err := layer.CleanupOne(CleanupOneRequest{
		Context: ctx, IOIndex: 0, Method: CleanupMethodPreserveAbnormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Processed || result.Key != abnormalKey {
		t.Fatalf("CleanupOne result = %+v, want preserved key %+v", result, abnormalKey)
	}
	if _, err := ios[0].Read(physicalio.ReadRequest{Context: ctx, Key: abnormalKey}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abnormal chunk remains in active storage: %v", err)
	}
	if got := layer.cleanupCandidates(); len(got) != 0 {
		t.Fatalf("preserved abnormal chunk remained queued: %+v", got)
	}
}

func TestCleanupOneReturnsNoWorkForUnmatchedDriveOrMethod(t *testing.T) {
	ctx := context.Background()
	created, err := New(NewRequest{IOs: makeIOs(t, 2)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := created.Layer.CleanupOne(CleanupOneRequest{
		Context: ctx, IOIndex: 0, Method: CleanupMethodDeleteOldVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
}
