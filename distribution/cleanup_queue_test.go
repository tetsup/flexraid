package distribution

import (
	"context"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestWriteStripeRefreshesPerDriveCleanupQueue(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}

	request := WriteStripeRequest{
		Context: ctx, ObjectID: testObjectID(), StripeID: 3, ChunkSize: 16,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1},
		Data: []byte("first version"),
	}
	if _, err := created.Layer.WriteStripe(request); err != nil {
		t.Fatal(err)
	}
	if got := created.Layer.cleanupCandidates(); len(got) != 0 {
		t.Fatalf("first version unexpectedly queued cleanup candidates: %+v", got)
	}
	request.Data = []byte("second version")
	if _, err := created.Layer.WriteStripe(request); err != nil {
		t.Fatal(err)
	}
	got := created.Layer.cleanupCandidates()
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want one per drive: %+v", len(got), got)
	}
	for _, candidate := range got {
		if candidate.objectID != request.ObjectID || candidate.stripeID != request.StripeID {
			t.Errorf("candidate has wrong stripe identity: %+v", candidate)
		}
		if candidate.method != cleanupDeleteOldVersion {
			t.Errorf("candidate method = %v, want DeleteOldVersion", candidate.method)
		}
	}
	if got[0].ioIndex == got[1].ioIndex {
		t.Fatalf("candidates were not split by drive: %+v", got)
	}

	created.Layer.mapMu.Lock()
	created.Layer.refreshStripeCleanupQueueLocked(stripeIDKey{objectID: request.ObjectID, stripeID: request.StripeID})
	created.Layer.mapMu.Unlock()
	if after := created.Layer.cleanupCandidates(); len(after) != 2 {
		t.Fatalf("refresh duplicated candidates: %+v", after)
	}
}

func TestNewRebuildsCleanupQueuePerStripe(t *testing.T) {
	ctx := context.Background()
	ios := makeIOs(t, 2)
	created, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	request := WriteStripeRequest{
		Context: ctx, ObjectID: testObjectID(), StripeID: storage.StripeID(7), ChunkSize: 16,
		Layout: Layout{Type: LayoutMirror, NumChunks: 2}, IOIndexes: []int{0, 1}, Data: []byte("old"),
	}
	if _, err := created.Layer.WriteStripe(request); err != nil {
		t.Fatal(err)
	}
	request.Data = []byte("new")
	if _, err := created.Layer.WriteStripe(request); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	got := restarted.Layer.cleanupCandidates()
	if len(got) != 2 {
		t.Fatalf("startup rebuilt %d candidates, want two drive candidates: %+v", len(got), got)
	}
	for _, candidate := range got {
		if candidate.method != cleanupDeleteOldVersion {
			t.Errorf("startup candidate method = %v, want DeleteOldVersion", candidate.method)
		}
	}
}

func TestCleanupQueueClassifiesNewerVersionAsAbnormal(t *testing.T) {
	layer := &Distribution{
		objects:  make(map[shared.ObjectID]map[storage.StripeID]*stripeInfo),
		versions: make(map[stripeIDKey]map[storage.StripeVersion]*versionInfo),
		orphans:  make(map[shared.ObjectID][]chunkLocation), cleanupQueue: newCleanupQueue(),
	}
	id := testObjectID()
	key := stripeIDKey{objectID: id, stripeID: 5}
	layer.objects[id] = map[storage.StripeID]*stripeInfo{5: {version: 2}}
	layer.versions[key] = map[storage.StripeVersion]*versionInfo{
		1: {chunks: map[chunkNo]chunkLocation{0: {ioIndex: 0}}},
		2: {recoverable: true, chunks: map[chunkNo]chunkLocation{0: {ioIndex: 0}}},
		3: {chunks: map[chunkNo]chunkLocation{0: {ioIndex: 1}}},
	}
	layer.mapMu.Lock()
	layer.refreshStripeCleanupQueueLocked(key)
	layer.mapMu.Unlock()
	got := layer.cleanupCandidates()
	if len(got) != 2 {
		t.Fatalf("got candidates %+v, want old and abnormal drive entries", got)
	}
	methods := map[cleanupMethod]int{}
	for _, candidate := range got {
		methods[candidate.method]++
	}
	if methods[cleanupDeleteOldVersion] != 1 || methods[cleanupPreserveAbnormal] != 1 {
		t.Fatalf("candidate methods = %+v", methods)
	}
}
