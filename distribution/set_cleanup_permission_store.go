package distribution

import (
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
)

// SetCleanupPermissionStore attaches the Management-owned shared permission
// state used by both autonomous and Object-level cleanup.
func (d *Distribution) SetCleanupPermissionStore(store *shared.CleanupPermissionStore) error {
	if store == nil {
		return fmt.Errorf("cleanup permission store is required")
	}
	snapshot := store.Snapshot()
	if len(snapshot.Drives) != len(d.ios) {
		return fmt.Errorf("cleanup permission store has %d drives; want %d", len(snapshot.Drives), len(d.ios))
	}
	d.cleanupPermissionStore = store
	d.wakeCleanupWorker()
	return nil
}
