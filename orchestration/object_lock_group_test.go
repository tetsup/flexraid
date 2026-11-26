package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

func lockTestObjectID(value byte) shared.ObjectID {
	var id shared.ObjectID
	id[0] = value
	return id
}

func newTestLockGroup(t *testing.T, manager *objectLockManager, mode ObjectLockMode, ids ...shared.ObjectID) *objectLockGroup {
	t.Helper()
	group, err := manager.lockGroup(context.Background(), ids, mode)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func TestObjectLockGroupSortsAndDeduplicatesInitialIDs(t *testing.T) {
	manager := newObjectLockManager()
	group := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(3), lockTestObjectID(1), lockTestObjectID(3), lockTestObjectID(2))
	defer group.Unlock()
	want := []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(2), lockTestObjectID(3)}
	if len(group.order) != len(want) {
		t.Fatalf("held count = %d, want %d", len(group.order), len(want))
	}
	for i := range want {
		if group.order[i] != want[i] {
			t.Fatalf("order[%d] = %v, want %v", i, group.order[i], want[i])
		}
	}
}

func TestObjectLockGroupsAllowIndependentObjectsConcurrently(t *testing.T) {
	manager := newObjectLockManager()
	first := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(1))
	defer first.Unlock()

	acquired := make(chan struct{})
	go func() {
		second, err := manager.lockGroup(context.Background(), []shared.ObjectID{lockTestObjectID(2)}, ObjectLockWrite)
		if err == nil {
			second.Unlock()
			close(acquired)
		}
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("independent Object lock group was serialized")
	}
}

func TestObjectReadLocksShareAndExcludeWriters(t *testing.T) {
	manager := newObjectLockManager()
	first := newTestLockGroup(t, manager, ObjectLockRead, lockTestObjectID(1))
	defer first.Unlock()
	second := newTestLockGroup(t, manager, ObjectLockRead, lockTestObjectID(1))
	defer second.Unlock()

	writerAcquired := make(chan struct{})
	go func() {
		writer, err := manager.lockGroup(context.Background(), []shared.ObjectID{lockTestObjectID(1)}, ObjectLockWrite)
		if err == nil {
			close(writerAcquired)
			writer.Unlock()
		}
	}()
	select {
	case <-writerAcquired:
		t.Fatal("writer acquired while readers held the Object")
	case <-time.After(30 * time.Millisecond):
	}
	first.Unlock()
	second.Unlock()
	select {
	case <-writerAcquired:
	case <-time.After(time.Second):
		t.Fatal("writer did not acquire after readers released")
	}
}

func TestObjectLockGroupAddReleasesSuffixBeforeReacquiringInOrder(t *testing.T) {
	manager := newObjectLockManager()
	group := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(1), lockTestObjectID(3))
	defer group.Unlock()

	blocker := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(2))
	addDone := make(chan error, 1)
	go func() { addDone <- group.Add(context.Background(), lockTestObjectID(2)) }()

	// Add must release Object 3 while waiting for the newly inserted Object 2.
	suffixAcquired := make(chan struct{})
	go func() {
		suffix, err := manager.lockGroup(context.Background(), []shared.ObjectID{lockTestObjectID(3)}, ObjectLockWrite)
		if err == nil {
			close(suffixAcquired)
			suffix.Unlock()
		}
	}()
	select {
	case <-suffixAcquired:
	case <-time.After(time.Second):
		t.Fatal("higher Object lock was not released before waiting for lower Object")
	}

	blocker.Unlock()
	select {
	case err := <-addDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Add did not complete after inserted Object became available")
	}
	want := []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(2), lockTestObjectID(3)}
	for i := range want {
		if group.order[i] != want[i] {
			t.Fatalf("order[%d] = %v, want %v", i, group.order[i], want[i])
		}
	}
}

func TestObjectLockGroupAddExistingObjectIsNoop(t *testing.T) {
	manager := newObjectLockManager()
	group := newTestLockGroup(t, manager, ObjectLockRead, lockTestObjectID(1))
	defer group.Unlock()
	if err := group.Add(context.Background(), lockTestObjectID(1)); err != nil {
		t.Fatal(err)
	}
	if len(group.order) != 1 {
		t.Fatalf("held count = %d, want 1", len(group.order))
	}
}

func TestObjectLockGroupUnlockIsIdempotentAndRetiresEntries(t *testing.T) {
	manager := newObjectLockManager()
	group := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(1), lockTestObjectID(2))
	group.Unlock()
	group.Unlock()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.entries) != 0 {
		t.Fatalf("lock registry retains %d entries after final unlock", len(manager.entries))
	}
}

func TestNestedObjectLockGroupReusesOuterGroup(t *testing.T) {
	layer := &Orchestration{objectLocks: newObjectLockManager()}
	first, err := layer.LockObjectGroup(LockObjectGroupRequest{
		Context:   context.Background(),
		ObjectIDs: []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(3)},
		Mode:      ObjectLockWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()

	nested, err := layer.LockObjectGroup(LockObjectGroupRequest{
		Context:   first.Context,
		ObjectIDs: []shared.ObjectID{lockTestObjectID(2)},
		Mode:      ObjectLockWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	nested.Unlock()

	group, ok := first.Context.Value(objectLockGroupContextKey{}).(*objectLockGroup)
	if !ok {
		t.Fatal("outer context lost its lock group")
	}
	want := []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(2), lockTestObjectID(3)}
	for i := range want {
		if group.order[i] != want[i] {
			t.Fatalf("order[%d] = %v, want %v", i, group.order[i], want[i])
		}
	}
}

func TestNestedObjectLockGroupCannotChangeMode(t *testing.T) {
	layer := &Orchestration{objectLocks: newObjectLockManager()}
	first, err := layer.LockObjectGroup(LockObjectGroupRequest{Context: context.Background(), Mode: ObjectLockRead})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()
	_, err = layer.LockObjectGroup(LockObjectGroupRequest{Context: first.Context, Mode: ObjectLockWrite})
	if err == nil {
		t.Fatal("nested group changed its lock mode")
	}
}

func TestObjectLockAcquisitionCanBeCanceled(t *testing.T) {
	manager := newObjectLockManager()
	held := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(1))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.acquire(ctx, lockTestObjectID(1), ObjectLockWrite)
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled acquisition succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled acquisition did not return")
	}
	held.Unlock()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.entries) != 0 {
		t.Fatalf("lock registry retains %d entries after cancellation and unlock", len(manager.entries))
	}
}
