package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

func testObjectID(value byte) shared.ObjectID {
	var id shared.ObjectID
	id[0] = value
	return id
}

func TestObjectLockGroupAcquiresAdditionalIDsInOrder(t *testing.T) {
	manager := newObjectLockManager()
	group, err := manager.lockGroup(context.Background(), testObjectID(1), testObjectID(3))
	if err != nil {
		t.Fatal(err)
	}
	defer group.Unlock()

	if err := group.Add(context.Background(), testObjectID(2)); err != nil {
		t.Fatal(err)
	}
	if len(group.order) != 3 {
		t.Fatalf("held lock count = %d, want 3", len(group.order))
	}
	for i, want := range []shared.ObjectID{testObjectID(1), testObjectID(2), testObjectID(3)} {
		if group.order[i] != want {
			t.Fatalf("lock order[%d] = %v, want %v", i, group.order[i], want)
		}
	}
}

func TestObjectLockGroupSerializesGroupsWithinProcess(t *testing.T) {
	manager := newObjectLockManager()
	first, err := manager.lockGroup(context.Background(), testObjectID(1))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()

	acquired := make(chan struct{})
	go func() {
		second, lockErr := manager.lockGroup(context.Background(), testObjectID(2))
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
