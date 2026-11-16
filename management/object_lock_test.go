package management

import (
	"context"
	"testing"
	"time"
)

func TestObjectLockManagerAllowsUnrelatedObjectsConcurrently(t *testing.T) {
	manager := NewObjectLockManager()
	releaseA, err := manager.AcquireOperation(context.Background(), []ObjectLockRequest{{ObjectID:"a", Mode:ObjectLockExclusive}})
	if err != nil { t.Fatal(err) }
	defer releaseA()
	acquired := make(chan func(), 1)
	go func() {
		release, err := manager.AcquireOperation(context.Background(), []ObjectLockRequest{{ObjectID:"b", Mode:ObjectLockExclusive}})
		if err == nil { acquired <- release }
	}()
	select {
	case release := <-acquired: release()
	case <-time.After(time.Second): t.Fatal("unrelated object lock was unnecessarily serialized")
	}
}

func TestObjectLockManagerSharedAndExclusive(t *testing.T) {
	manager := NewObjectLockManager()
	releaseRead, err := manager.AcquireOperation(context.Background(), []ObjectLockRequest{{ObjectID:"file", Mode:ObjectLockShared}})
	if err != nil { t.Fatal(err) }
	writer := make(chan func(), 1)
	go func() {
		release, err := manager.AcquireOperation(context.Background(), []ObjectLockRequest{{ObjectID:"file", Mode:ObjectLockExclusive}})
		if err == nil { writer <- release }
	}()
	select {
	case <-writer: t.Fatal("exclusive lock passed an active reader")
	case <-time.After(30*time.Millisecond):
	}
	releaseRead()
	select {
	case release := <-writer: release()
	case <-time.After(time.Second): t.Fatal("exclusive lock did not proceed after reader released")
	}
}

func TestObjectLockManagerMaintenanceDrainsOperations(t *testing.T) {
	manager := NewObjectLockManager()
	release, err := manager.AcquireOperation(context.Background(), nil)
	if err != nil { t.Fatal(err) }
	maintenance := make(chan func(), 1)
	go func() {
		unlock, err := manager.AcquireMaintenance(context.Background())
		if err == nil { maintenance <- unlock }
	}()
	select {
	case <-maintenance: t.Fatal("maintenance passed an active operation")
	case <-time.After(30*time.Millisecond):
	}
	release()
	select {
	case unlock := <-maintenance: unlock()
	case <-time.After(time.Second): t.Fatal("maintenance did not acquire after drain")
	}
}

func TestObjectLockManagerMultiObjectAcquisitionIsAtomic(t *testing.T) {
	manager := NewObjectLockManager()
	releaseA, err := manager.AcquireOperation(context.Background(), []ObjectLockRequest{{ObjectID:"a", Mode:ObjectLockExclusive}})
	if err != nil { t.Fatal(err) }
	defer releaseA()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = manager.AcquireOperation(ctx, []ObjectLockRequest{
		{ObjectID:"b", Mode:ObjectLockExclusive},
		{ObjectID:"a", Mode:ObjectLockExclusive},
	})
	if err == nil { t.Fatal("expected acquisition to wait for object a") }
	// Object b must not have been partially acquired.
	releaseB, err := manager.AcquireOperation(context.Background(), []ObjectLockRequest{{ObjectID:"b", Mode:ObjectLockExclusive}})
	if err != nil { t.Fatalf("object b was partially held: %v", err) }
	releaseB()
}
