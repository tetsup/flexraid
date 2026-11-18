package distribution

import (
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestClassifyStripePartitionsChunksByVersion(t *testing.T) {
	objectID := testObjectID()
	location := func(version storage.StripeVersion, ioIndex int, index uint8) chunkLocation {
		return chunkLocation{ioIndex: ioIndex, key: storage.FileKey{
			ObjectID: objectID, StripeID: 7, StripeVersion: version,
			LayoutID: storage.LayoutID{Type: storage.LayoutType(LayoutMirror), DriveCount: 2, Role: storage.ChunkRoleData, Index: index},
		}}
	}
	versions := map[storage.StripeVersion]*versionInfo{
		1: {recoverable: false, chunks: map[chunkNo]chunkLocation{0: location(1, 0, 1)}},
		2: {recoverable: true, chunks: map[chunkNo]chunkLocation{0: location(2, 1, 1)}},
		3: {recoverable: false, chunks: map[chunkNo]chunkLocation{0: location(3, 2, 1)}},
	}
	orphans := []chunkLocation{location(1, 3, 2), location(2, 4, 2), location(4, 5, 1)}
	classified, ok := classifyStripe(versions, orphans, &stripeInfo{version: 2, chunks: versions[2].chunks})
	if !ok {
		t.Fatal("expected a classifiable Stripe")
	}
	if len(classified.Current) != 1 || classified.Current[0].ioIndex != 1 {
		t.Fatalf("Current = %+v", classified.Current)
	}
	if len(classified.OldVersions) != 2 {
		t.Fatalf("OldVersions = %+v", classified.OldVersions)
	}
	if len(classified.Abnormal) != 3 {
		t.Fatalf("Abnormal = %+v", classified.Abnormal)
	}
}

func TestClassifyStripeDefersWithoutRecoverableCurrent(t *testing.T) {
	versions := map[storage.StripeVersion]*versionInfo{1: {recoverable: false, chunks: map[chunkNo]chunkLocation{0: {ioIndex: 0}}}}
	classified, ok := classifyStripe(versions, []chunkLocation{{ioIndex: 1}}, nil)
	if ok {
		t.Fatal("expected classification to be deferred")
	}
	if len(classified.Current)+len(classified.OldVersions)+len(classified.Abnormal) != 0 {
		t.Fatalf("deferred Stripe returned classified chunks: %+v", classified)
	}
}

func TestRefreshStripeCleanupQueueClearsCandidatesWhenDeferred(t *testing.T) {
	objectID := shared.ObjectID{1}
	key := stripeIDKey{objectID: objectID, stripeID: 3}
	candidate := cleanupCandidateKey{ioIndex: 0, objectID: objectID, stripeID: 3, method: cleanupDeleteOldVersion}
	layer := &Distribution{
		objects: map[shared.ObjectID]map[storage.StripeID]*stripeInfo{objectID: {3: {version: 2}}},
		versions: map[stripeIDKey]map[storage.StripeVersion]*versionInfo{
			key: {1: {recoverable: false, chunks: map[chunkNo]chunkLocation{0: {ioIndex: 0}}}},
		},
		orphans:      map[shared.ObjectID][]chunkLocation{},
		cleanupQueue: &cleanupQueue{pending: map[cleanupCandidateKey]struct{}{candidate: {}}, inFlight: make(map[cleanupCandidateKey]struct{})},
	}
	layer.mapMu.Lock()
	layer.refreshStripeCleanupQueueLocked(key)
	layer.mapMu.Unlock()
	if got := layer.cleanupCandidates(); len(got) != 0 {
		t.Fatalf("stale candidates remain after deferral: %+v", got)
	}
}
