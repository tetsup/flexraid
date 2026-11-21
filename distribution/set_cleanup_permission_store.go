package distribution

import (
	"fmt"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

// SetCleanupPermissionStore attaches the Management-owned shared permission
// state used by both autonomous and Object-level cleanup.
func (d *Distribution) SetCleanupPermissionStore(store *shared.CleanupPermissionStore) (resultErr error) {
	started := time.Now()
	defer func(){ logOperation(d, "SetCleanupPermissionStore", started, resultErr) }()
	if store == nil {
		return fmt.Errorf("cleanup permission store is required")
	}
	if d.cleanupPermissionStore == store {
		return nil
	}
	snapshot := store.Snapshot()
	if len(snapshot.Drives) != len(d.ios) {
		return fmt.Errorf("cleanup permission store has %d drives; want %d", len(snapshot.Drives), len(d.ios))
	}
	d.cleanupWorkerMu.Lock()
	defer d.cleanupWorkerMu.Unlock()
	if d.cleanupWorkerCancel != nil && d.cleanupPermissionStore != store {
		return fmt.Errorf("cannot replace cleanup permission store while worker is running")
	}
	d.cleanupPermissionStore = store
	d.wakeCleanupWorker()
	return nil
}
