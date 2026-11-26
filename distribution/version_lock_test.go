package distribution

import (
	"runtime"
	"testing"
	"time"

	"github.com/tetsup/flexraid/storage"
)

func testVersionLockKey(version storage.StripeVersion) versionLockKey {
	return versionLockKey{objectID: testObjectID(), stripeID: 7, version: version}
}

func waitForVersionLockRefs(t *testing.T, d *Distribution, key versionLockKey, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.versionLocksMu.Lock()
		entry := d.versionLocks[key]
		var refs uint64
		if entry != nil {
			refs = entry.refs
		}
		d.versionLocksMu.Unlock()
		if refs == want {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("version lock refs for %+v did not reach %d", key, want)
}

func TestVersionLockAllowsConcurrentReaders(t *testing.T) {
	d := &Distribution{}
	key := testVersionLockKey(1)

	unlockFirst := d.lockVersionRead(key.objectID, key.stripeID, key.version)
	unlockSecond := d.lockVersionRead(key.objectID, key.stripeID, key.version)
	waitForVersionLockRefs(t, d, key, 2)

	unlockFirst()
	unlockSecond()

	d.versionLocksMu.Lock()
	defer d.versionLocksMu.Unlock()
	if _, exists := d.versionLocks[key]; exists {
		t.Fatal("idle version lock entry was not removed")
	}
}

func TestVersionLockWriterWaitsForReaders(t *testing.T) {
	d := &Distribution{}
	key := testVersionLockKey(2)
	unlockFirst := d.lockVersionRead(key.objectID, key.stripeID, key.version)
	unlockSecond := d.lockVersionRead(key.objectID, key.stripeID, key.version)

	acquired := make(chan func(), 1)
	go func() {
		acquired <- d.lockVersionWrite(key.objectID, key.stripeID, key.version)
	}()
	waitForVersionLockRefs(t, d, key, 3)
	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("writer acquired an exclusive lock while readers were active")
	default:
	}

	unlockFirst()
	unlockSecond()
	select {
	case unlock := <-acquired:
		unlock()
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not acquire the lock after readers released it")
	}
}

func TestVersionLockReaderWaitsForWriter(t *testing.T) {
	d := &Distribution{}
	key := testVersionLockKey(3)
	unlockWriter := d.lockVersionWrite(key.objectID, key.stripeID, key.version)

	acquired := make(chan func(), 1)
	go func() {
		acquired <- d.lockVersionRead(key.objectID, key.stripeID, key.version)
	}()
	waitForVersionLockRefs(t, d, key, 2)
	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("reader acquired a shared lock while a writer was active")
	default:
	}

	unlockWriter()
	select {
	case unlock := <-acquired:
		unlock()
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not acquire the lock after writer released it")
	}
}

func TestVersionLockRegistryRetainsEntryForWaiters(t *testing.T) {
	d := &Distribution{}
	key := testVersionLockKey(4)
	unlockHolder := d.lockVersionWrite(key.objectID, key.stripeID, key.version)

	d.versionLocksMu.Lock()
	originalEntry := d.versionLocks[key]
	d.versionLocksMu.Unlock()
	if originalEntry == nil {
		t.Fatal("lock entry was not registered")
	}

	acquired := make(chan func(), 1)
	go func() {
		acquired <- d.lockVersionRead(key.objectID, key.stripeID, key.version)
	}()
	waitForVersionLockRefs(t, d, key, 2)

	unlockHolder()
	var unlockWaiter func()
	select {
	case unlockWaiter = <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("waiting reader did not acquire the lock")
	}
	d.versionLocksMu.Lock()
	currentEntry := d.versionLocks[key]
	refs := uint64(0)
	if currentEntry != nil {
		refs = currentEntry.refs
	}
	d.versionLocksMu.Unlock()
	if currentEntry != originalEntry {
		t.Fatal("registry replaced an entry while a waiter was pending")
	}
	if refs != 1 {
		t.Fatalf("entry refs = %d, want 1 active reader", refs)
	}

	unlockWaiter()
	d.versionLocksMu.Lock()
	defer d.versionLocksMu.Unlock()
	if _, exists := d.versionLocks[key]; exists {
		t.Fatal("entry remained after its last holder released it")
	}
}

func TestVersionLockKeysAreIndependent(t *testing.T) {
	d := &Distribution{}
	base := testVersionLockKey(5)
	otherVersion := base
	otherVersion.version++
	otherStripe := base
	otherStripe.stripeID++
	otherObject := base
	otherObject.objectID[0]++

	unlockBase := d.lockVersionWrite(base.objectID, base.stripeID, base.version)
	defer unlockBase()

	for _, key := range []versionLockKey{otherVersion, otherStripe, otherObject} {
		acquired := make(chan func(), 1)
		go func(key versionLockKey) {
			acquired <- d.lockVersionWrite(key.objectID, key.stripeID, key.version)
		}(key)
		select {
		case unlock := <-acquired:
			unlock()
		case <-time.After(3 * time.Second):
			t.Fatalf("independent key %+v was blocked by %+v", key, base)
		}
	}
}
