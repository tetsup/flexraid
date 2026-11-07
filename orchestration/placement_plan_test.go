package orchestration

import (
	"reflect"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

func TestPlanPlacementMirrorBoundary(t *testing.T) {
	plan, err := planPlacement(3072)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Layout.Mode != layoutModeMirror || plan[0].DataSize != 3072 {
		t.Fatalf("unexpected mirror plan: %#v", plan)
	}
	plan, err = planPlacement(3073)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Layout.Mode != layoutModeStripe || plan[0].DataSize != 3073 {
		t.Fatalf("unexpected parity plan: %#v", plan)
	}
}

func TestPlanPlacementOverrideAndMaximum(t *testing.T) {
	policy := cfg.Placement{DiskCount: 5, DataChunks: 4, ParityChunks: 1, MaxChunkSize: 8192}
	for _, size := range []uint64{3073, 11264, 11265, 100000, 10 << 20} {
		plan, err := planPlacement(size, policy)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if len(plan) < 1 {
			t.Fatalf("empty plan for %d", size)
		}
		for i, stripe := range plan {
			if i > 0 && (stripe.ChunkSize < 4096 || stripe.ChunkSize > 8192 || stripe.ChunkSize%4096 != 0) {
				t.Fatalf("stripe %d chunk size %d", i, stripe.ChunkSize)
			}
		}
	}
}

func TestPlanPlacementRejectsInvalidOverride(t *testing.T) {
	_, err := planPlacement(9000, cfg.Placement{DiskCount: 1, DataChunks: 3, ParityChunks: 1, MaxChunkSize: 2 * 1024 * 1024})
	if err == nil {
		t.Fatal("expected invalid policy")
	}
}

func TestSelectIOIndexesUsesDescendingCapacityAndExactCount(t *testing.T) {
	got, err := selectIOIndexes([]shared.Capacity{{AvailableBytes: 100}, {AvailableBytes: 900}, {AvailableBytes: 500}, {AvailableBytes: 900}, {AvailableBytes: 1}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 3, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("indexes = %v, want %v", got, want)
	}
}

func TestLocateFileOffsetUsesReservedFirstStripe(t *testing.T) {
	h := header{Layout: layout{Mode: layoutModeStripe, ChunkSize: 8192, DataChunks: 3, ParityChunks: 1}}
	first, local, err := locateFileOffset(h, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.StripeID != 0 || first.ChunkSize != 4096 || local != 1024 || first.Capacity != 11264 {
		t.Fatalf("first location = %+v local=%d", first, local)
	}
	next, local, err := locateFileOffset(h, 11264)
	if err != nil {
		t.Fatal(err)
	}
	if next.StripeID != 1 || next.ChunkSize != 8192 || local != 0 || next.FileOffset != 11264 {
		t.Fatalf("next location = %+v local=%d", next, local)
	}
	next, local, err = locateFileOffset(h, 11264+8192*3+7)
	if err != nil {
		t.Fatal(err)
	}
	if next.StripeID != 2 || local != 7 {
		t.Fatalf("later location = %+v local=%d", next, local)
	}
}
