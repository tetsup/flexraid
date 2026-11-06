package orchestration

import (
	"fmt"
	"time"

	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

// updateHeader updates logical metadata and recalculates placement whenever
// the logical file size changes. Persistence operations never replan placement.
func updateHeader(current header, size uint64, now time.Time, overrides ...cfg.Placement) (header, error) {
	policy := cfg.DefaultPlacement()
	if len(overrides) > 1 {
		return header{}, fmt.Errorf("at most one placement override")
	}
	if len(overrides) == 1 {
		policy = overrides[0]
	}
	if err := validatePersistencePlacement(policy); err != nil {
		return header{}, err
	}

	next := current
	replan := current.Layout.ChunkSize == 0 || size != current.Size
	if replan {
		plan, err := planPlacement(size, policy)
		if err != nil {
			return header{}, err
		}
		next.Layout = plan[0].Layout
		if len(plan) > 1 {
			next.Layout.ChunkSize = plan[1].ChunkSize
		} else {
			next.Layout.ChunkSize = cfg.MinimumChunkSize
		}
	}
	next.Size = size
	next.Mtime = now
	next.Ctime = now
	return next, nil
}
