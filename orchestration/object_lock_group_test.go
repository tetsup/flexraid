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
	first, err := layer.NewObjectLockGroup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Add(lockTestObjectID(1)); err != nil {
		t.Fatal(err)
	}
	if err := first.Add(lockTestObjectID(3)); err != nil {
		t.Fatal(err)
	}
	if err := first.Lock(ObjectLockWrite); err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()

	nested, ok := layer.ObjectLockGroupFromContext(first.Context())
	if !ok {
		t.Fatal("outer context lost its lock group")
	}
	if err := nested.Add(lockTestObjectID(2)); err != nil {
		t.Fatal(err)
	}

	want := []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(2), lockTestObjectID(3)}
	for i := range want {
		if first.group.order[i] != want[i] {
			t.Fatalf("order[%d] = %v, want %v", i, first.group.order[i], want[i])
		}
	}
}

func TestObjectLockGroupRelocksWithDifferentMode(t *testing.T) {
	layer := &Orchestration{objectLocks: newObjectLockManager()}
	group, err := layer.NewObjectLockGroup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Add(lockTestObjectID(1)); err != nil {
		t.Fatal(err)
	}
	if err := group.Lock(ObjectLockRead); err != nil {
		t.Fatal(err)
	}
	if err := group.Lock(ObjectLockWrite); err == nil {
		t.Fatal("Lock succeeded while group was already locked")
	}
	group.Unlock()
	if err := group.Lock(ObjectLockWrite); err != nil {
		t.Fatalf("Lock after Unlock with a different mode: %v", err)
	}
	group.Unlock()
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
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		remaining := len(manager.entries)
		manager.mu.Unlock()
		if remaining == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock registry retains %d entries after cancellation and unlock", remaining)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestObjectLockGroupCanBeRelockedAfterCanceledAddition(t *testing.T) {
	manager := newObjectLockManager()
	group := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(1), lockTestObjectID(3))
	blocker := newTestLockGroup(t, manager, ObjectLockWrite, lockTestObjectID(2))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := group.Add(ctx, lockTestObjectID(2)); err == nil {
		t.Fatal("addition succeeded despite context deadline")
	}
	if err := group.Add(context.Background(), lockTestObjectID(4)); err != nil {
		t.Fatalf("group did not recover after failed extension: %v", err)
	}

	blocker.Unlock()
	group.Unlock()
	if err := group.Lock(context.Background(), ObjectLockRead); err != nil {
		t.Fatalf("relock after failed extension: %v", err)
	}
	group.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		remaining := len(manager.entries)
		manager.mu.Unlock()
		if remaining == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock registry retains %d entries after cleanup", remaining)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestObjectLockGroupCanBeRelockedWithDifferentMode(t *testing.T) {
	manager := newObjectLockManager()
	group := newObjectLockGroup(manager)
	if err := group.Add(context.Background(), lockTestObjectID(2)); err != nil {
		t.Fatal(err)
	}
	if err := group.Add(context.Background(), lockTestObjectID(1)); err != nil {
		t.Fatal(err)
	}
	if err := group.Lock(context.Background(), ObjectLockRead); err != nil {
		t.Fatal(err)
	}
	if err := group.Lock(context.Background(), ObjectLockWrite); err == nil {
		t.Fatal("Lock succeeded while group was already locked")
	}
	group.Unlock()
	if len(group.targets) != 2 {
		t.Fatalf("target count after Unlock = %d, want 2", len(group.targets))
	}
	if err := group.Lock(context.Background(), ObjectLockWrite); err != nil {
		t.Fatalf("Lock with a new mode: %v", err)
	}
	group.Unlock()
}

func TestObjectLockGroupAddBeforeAndAfterLock(t *testing.T) {
	manager := newObjectLockManager()
	group := newObjectLockGroup(manager)
	if err := group.Add(context.Background(), lockTestObjectID(3)); err != nil {
		t.Fatal(err)
	}
	if err := group.Lock(context.Background(), ObjectLockWrite); err != nil {
		t.Fatal(err)
	}
	if err := group.Add(context.Background(), lockTestObjectID(1)); err != nil {
		t.Fatal(err)
	}
	want := []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(3)}
	if len(group.order) != len(want) {
		t.Fatalf("held count = %d, want %d", len(group.order), len(want))
	}
	for i := range want {
		if group.order[i] != want[i] {
			t.Fatalf("order[%d] = %v, want %v", i, group.order[i], want[i])
		}
	}
	group.Unlock()
}

func TestPublicObjectLockGroupAPI(t *testing.T) {
	layer := &Orchestration{objectLocks: newObjectLockManager()}
	group, err := layer.NewObjectLockGroup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fromContext, ok := layer.ObjectLockGroupFromContext(group.Context())
	if !ok {
		t.Fatal("group context does not expose the existing lock group")
	}
	if err := fromContext.Add(lockTestObjectID(2)); err != nil {
		t.Fatalf("add duplicate through context group: %v", err)
	}
	if err := group.Add(lockTestObjectID(2)); err != nil {
		t.Fatal(err)
	}
	if err := group.Add(lockTestObjectID(1)); err != nil {
		t.Fatal(err)
	}
	if err := group.Lock(ObjectLockRead); err != nil {
		t.Fatal(err)
	}
	group.Unlock()
	if err := group.Lock(ObjectLockWrite); err != nil {
		t.Fatal(err)
	}
	group.Unlock()
}
