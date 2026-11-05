package orchestration

import (
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"testing"
)

func TestPlanPlacementMirrorBoundary(t *testing.T) {
	p, e := planPlacement(3072)
	if e != nil {
		t.Fatal(e)
	}
	if len(p) != 1 || p[0].Layout.Mode != layoutModeMirror || len(p[0].IOIndexes) != 2 {
		t.Fatalf("unexpected plan: %#v", p)
	}
}
func TestPlanPlacementOverrideAndMaximum(t *testing.T) {
	c := cfg.Placement{DiskCount: 5, DataChunks: 4, ParityChunks: 1, MaxChunkSize: 8192}
	for _, size := range []uint64{3073, 11264, 11265, 100000, 10 << 20} {
		p, e := planPlacement(size, c)
		if e != nil {
			t.Fatalf("size %d: %v", size, e)
		}
		if len(p) < 1 {
			t.Fatalf("empty plan for %d", size)
		}
		for i, s := range p {
			if len(s.IOIndexes) != 5 {
				t.Fatalf("stripe %d indexes %v", i, s.IOIndexes)
			}
			if i > 0 && (s.ChunkSize < 4096 || s.ChunkSize > 8192 || s.ChunkSize%4096 != 0) {
				t.Fatalf("stripe %d chunk size %d", i, s.ChunkSize)
			}
		}
	}
}
func TestPlanPlacementRejectsInvalidOverride(t *testing.T) {
	_, e := planPlacement(9000, cfg.Placement{DiskCount: 1, DataChunks: 3, ParityChunks: 1, MaxChunkSize: 2 * 1024 * 1024})
	if e == nil {
		t.Fatal("expected invalid policy")
	}
}
