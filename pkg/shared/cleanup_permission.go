package shared

import (
	"fmt"
	"sync"
)

// DriveCleanupPermission controls destructive cleanup operations on one drive.
type DriveCleanupPermission struct {
	DeleteAllowed   bool
	PreserveAllowed bool
}

// CleanupPermissionSnapshot is an immutable copy of the current drive permissions.
type CleanupPermissionSnapshot struct {
	Generation uint64
	Drives     []DriveCleanupPermission
}

// CleanupPermissionStore shares one permission state between Management,
// Orchestration, and Distribution. Management is the sole writer.
type CleanupPermissionStore struct {
	mu         sync.RWMutex
	generation uint64
	drives     []DriveCleanupPermission
}

// NewCleanupPermissionStore creates a store with an initial drive-ordered snapshot.
func NewCleanupPermissionStore(drives []DriveCleanupPermission) *CleanupPermissionStore {
	return &CleanupPermissionStore{
		generation: 1,
		drives:     append([]DriveCleanupPermission(nil), drives...),
	}
}

// Snapshot returns a detached copy and its generation.
func (s *CleanupPermissionStore) Snapshot() CleanupPermissionSnapshot {
	if s == nil {
		return CleanupPermissionSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CleanupPermissionSnapshot{
		Generation: s.generation,
		Drives:     append([]DriveCleanupPermission(nil), s.drives...),
	}
}

// Replace publishes a complete drive-ordered permission snapshot.
func (s *CleanupPermissionStore) Replace(drives []DriveCleanupPermission) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(drives) != len(s.drives) {
		return fmt.Errorf("cleanup permission drive count mismatch: got %d drives, want %d", len(drives), len(s.drives))
	}
	s.drives = append(s.drives[:0], drives...)
	s.generation++
	return nil
}
