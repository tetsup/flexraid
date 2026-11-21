package orchestration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
)

func TestGetMetricsTracksLogicalOperationsAndReadErrors(t *testing.T) {
	_, distributed := newEmptyDistribution(t)
	createdRoot, err := CreateRoot(CreateRootRequest{Distribution: distributed})
	if err != nil {
		t.Fatal(err)
	}
	layer := createdRoot.Layer
	file, err := layer.CreateLocked(CreateRequest{ParentID: createdRoot.RootID, Name: "metrics-file"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layer.LookupLocked(LookupRequest{ParentID: createdRoot.RootID, Name: "metrics-file"}); err != nil {
		t.Fatal(err)
	}
	if _, err := layer.LookupLocked(LookupRequest{ParentID: createdRoot.RootID, Name: "missing"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing Lookup unexpectedly succeeded")
	}

	firstGeometry, err := geometryFor(layer.objects[file.ObjectID].file.header, 0)
	if err != nil {
		t.Fatal(err)
	}
	missingID := testID("42345678-9abc-4def-8abc-1234567890ab")
	otherMissingID := testID("52345678-9abc-4def-8abc-1234567890ab")
	for _, objectID := range []shared.ObjectID{missingID, missingID, otherMissingID} {
		if _, readErr := layer.readStripeValidated(context.Background(), objectID, layer.objects[file.ObjectID].file.header, firstGeometry); !errors.Is(readErr, distribution.ErrStripeNotFound) {
			t.Fatalf("readStripeValidated(%s) = %v, want ErrStripeNotFound", objectID, readErr)
		}
	}

	got, err := layer.GetMetrics(GetMetricsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Operations["Create"].SuccessCount != 1 || got.Operations["Create"].FailureCount != 0 {
		t.Fatalf("Create operation metrics = %+v", got.Operations["Create"])
	}
	if got.Operations["Lookup"].SuccessCount != 1 || got.Operations["Lookup"].FailureCount != 1 ||
		got.Operations["Lookup"].Recent.ProcessedCount != 2 {
		t.Fatalf("Lookup operation metrics = %+v", got.Operations["Lookup"])
	}
	if got.ReadErrors.OccurrenceCount != 3 || got.ReadErrors.UniqueObjectCount != 2 ||
		len(got.ReadErrors.ByObject) != 2 || got.ReadErrors.Recent.OccurrenceCount != 3 ||
		got.ReadErrors.Recent.UniqueObjectCount != 2 {
		t.Fatalf("logical read error metrics = %+v", got.ReadErrors)
	}
	if got.RecoverableMissingFileCount != 0 || got.UnrecoverableMissingFileCount != 0 {
		t.Fatalf("healthy logical file classified missing: %+v", got)
	}

	// Returned maps and slices must not alias collector state.
	delete(got.Operations, "Create")
	got.ReadErrors.ByObject[0].OccurrenceCount = 999
	next, err := layer.GetMetrics(GetMetricsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if next.Operations["Create"].SuccessCount != 1 || next.ReadErrors.ByObject[0].OccurrenceCount == 999 {
		t.Fatalf("GetMetrics returned aliased state: %+v", next)
	}
}

func TestLogicalFileMissingStateUsesStripeRecoverability(t *testing.T) {
	fileID := testID("32345678-9abc-4def-8abc-1234567890ab")
	fileHeader := testHeader(objectKindFile, fileID, nil, nil, "file")
	fileHeader.Layout = layout{Mode: layoutModeMirror, ChunkSize: 64, DataChunks: 2}
	fileHeader.Size = 128

	cases := []struct {
		name            string
		stripes         []distribution.StripeVersionMetrics
		wantMissing     bool
		wantRecoverable bool
	}{
		{
			name: "current version missing one chunk but recoverable",
			stripes: []distribution.StripeVersionMetrics{{
				StripeID: 0, Current: true, Recoverable: true, Corrupted: true,
				ExpectedChunkCount: 2, PresentChunkCount: 1, MissingChunkIndexes: []uint8{1},
			}},
			wantMissing: true, wantRecoverable: true,
		},
		{
			name: "current version unrecoverable",
			stripes: []distribution.StripeVersionMetrics{{
				StripeID: 0, Current: true, Recoverable: false, Corrupted: true,
				ExpectedChunkCount: 2, PresentChunkCount: 0, MissingChunkIndexes: []uint8{0, 1},
			}},
			wantMissing: true, wantRecoverable: false,
		},
		{
			name: "no current version but recoverable version remains",
			stripes: []distribution.StripeVersionMetrics{{
				StripeID: 0, Recoverable: true,
			}},
			wantMissing: true, wantRecoverable: true,
		},
		{
			name:        "stripe absent",
			wantMissing: true, wantRecoverable: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMissing, gotRecoverable := logicalFileMissingState(fileHeader, distribution.ObjectMetrics{
				ObjectID: fileID, Stripes: tc.stripes,
			})
			if gotMissing != tc.wantMissing || gotRecoverable != tc.wantRecoverable {
				t.Fatalf("logicalFileMissingState() = (%v,%v), want (%v,%v)",
					gotMissing, gotRecoverable, tc.wantMissing, tc.wantRecoverable)
			}
		})
	}
}

func TestGetMetricsRequiresLiveContext(t *testing.T) {
	_, distributed := newEmptyDistribution(t)
	created, err := CreateRoot(CreateRootRequest{Distribution: distributed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.Layer.GetMetrics(GetMetricsRequest{}); err == nil {
		t.Fatal("GetMetrics accepted a nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := created.Layer.GetMetrics(GetMetricsRequest{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMetrics with canceled context = %v, want context.Canceled", err)
	}
}

func TestOrchestrationMetricsRecentWindowIsBounded(t *testing.T) {
	collector := newMetricsCollector()
	layer := &Orchestration{metrics: collector}
	objectA := testID("32345678-9abc-4def-8abc-1234567890ab")
	objectB := testID("42345678-9abc-4def-8abc-1234567890ab")
	for i := 0; i < recentMetricsLimit+5; i++ {
		var err error
		if i%2 == 0 {
			err = distribution.ErrStripeNotFound
		}
		collector.recordOperation("Read", time.Duration(i), err)
		if i%2 == 0 {
			layer.recordReadError(objectA, err)
		} else {
			layer.recordReadError(objectB, distribution.ErrDataCorrupted)
		}
	}
	operations, readErrors := collector.snapshot()
	recent := operations["Read"].Recent
	if recent.ProcessedCount != recentMetricsLimit || recent.SuccessCount != 500 || recent.FailureCount != 500 {
		t.Fatalf("recent operation window = %+v", recent)
	}
	if readErrors.Recent.OccurrenceCount != recentMetricsLimit || readErrors.Recent.UniqueObjectCount != 2 {
		t.Fatalf("recent read-error window = %+v", readErrors.Recent)
	}
}
