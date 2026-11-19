package shared

import "testing"

func TestCleanupPermissionStoreSnapshotsAreDetached(t *testing.T) {
	store := NewCleanupPermissionStore([]DriveCleanupPermission{{DeleteAllowed: true}})
	snapshot := store.Snapshot()
	snapshot.Drives[0].DeleteAllowed = false
	if !store.Snapshot().Drives[0].DeleteAllowed {
		t.Fatal("mutating a snapshot changed the shared store")
	}
}

func TestCleanupPermissionStoreReplacePublishesGeneration(t *testing.T) {
	store := NewCleanupPermissionStore([]DriveCleanupPermission{{}})
	initial := store.Snapshot()
	if err := store.Replace([]DriveCleanupPermission{{DeleteAllowed: true}}); err != nil {
		t.Fatal(err)
	}
	updated := store.Snapshot()
	if updated.Generation != initial.Generation+1 || !updated.Drives[0].DeleteAllowed {
		t.Fatalf("updated snapshot = %+v", updated)
	}
	if err := store.Replace([]DriveCleanupPermission{{DeleteAllowed: true}}); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Generation; got != updated.Generation {
		t.Fatalf("unchanged replacement advanced generation to %d", got)
	}
}

func TestCleanupPermissionStoreRejectsDriveCountChange(t *testing.T) {
	store := NewCleanupPermissionStore([]DriveCleanupPermission{{}})
	if err := store.Replace([]DriveCleanupPermission{{}, {}}); err == nil {
		t.Fatal("Replace accepted a different drive count")
	}
}
