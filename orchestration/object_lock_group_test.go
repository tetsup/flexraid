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

func TestObjectLockGroupAcquiresAdditionalIDsInOrder(t *testing.T) {
	manager := newObjectLockManager()
	group, err := manager.lockGroup(context.Background(), lockTestObjectID(1), lockTestObjectID(3))
	if err != nil {
		t.Fatal(err)
	}
	defer group.Unlock()

	if err := group.Add(context.Background(), lockTestObjectID(2)); err != nil {
		t.Fatal(err)
	}
	if len(group.order) != 3 {
		t.Fatalf("held lock count = %d, want 3", len(group.order))
	}
	for i, want := range []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(2), lockTestObjectID(3)} {
		if group.order[i] != want {
			t.Fatalf("lock order[%d] = %v, want %v", i, group.order[i], want)
		}
	}
}

func TestObjectLockGroupSerializesGroupsWithinProcess(t *testing.T) {
	manager := newObjectLockManager()
	first, err := manager.lockGroup(context.Background(), lockTestObjectID(1))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()

	acquired := make(chan struct{})
	go func() {
		second, lockErr := manager.lockGroup(context.Background(), lockTestObjectID(2))
		if lockErr == nil {
			close(acquired)
			second.Unlock()
		}
	}()

	select {
	case <-acquired:
		t.Fatal("second lock group acquired while first group was held")
	case <-time.After(20 * time.Millisecond):
	}
	first.Unlock()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second lock group did not acquire after first group was released")
	}
}

func TestNestedObjectLockGroupReusesOuterGroup(t *testing.T) {
	layer := &Orchestration{objectLocks: newObjectLockManager()}
	first, err := layer.LockObjectGroup(LockObjectGroupRequest{
		Context: context.Background(), ObjectIDs: []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()

	nested, err := layer.LockObjectGroup(LockObjectGroupRequest{
		Context: first.Context, ObjectIDs: []shared.ObjectID{lockTestObjectID(2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	nested.Unlock()

	group, ok := first.Context.Value(objectLockGroupContextKey{}).(*objectLockGroup)
	if !ok {
		t.Fatal("outer context lost its lock group")
	}
	if len(group.order) != 3 {
		t.Fatalf("outer group holds %d objects, want 3", len(group.order))
	}
	for i, want := range []shared.ObjectID{lockTestObjectID(1), lockTestObjectID(2), lockTestObjectID(3)} {
		if group.order[i] != want {
			t.Fatalf("lock order[%d] = %v, want %v", i, group.order[i], want)
		}
	}
}
