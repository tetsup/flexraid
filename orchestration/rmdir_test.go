package orchestration

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
)

func TestRmdirRemovesEmptyDirectoryAndDerivesParentNlink(t *testing.T) {
	fixture := newNamespaceFixture(t)
	directory, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if got := deriveTestNlink(t, fixture.layer, fixture.rootID); got != 3 {
		t.Fatalf("parent Nlink before Rmdir = %d, want 3", got)
	}

	if _, err := fixture.layer.Rmdir(RmdirRequest{ParentID: fixture.rootID, Name: "empty"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "empty"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lookup after Rmdir = %v, want not-exist", err)
	}
	if !fixture.layer.objects[directory.ObjectID].header.Deleted {
		t.Fatal("removed directory was not marked deleted")
	}
	if _, queued := fixture.layer.deletionQueue[directory.ObjectID]; !queued {
		t.Fatal("removed directory was not queued for cleanup")
	}
	if got := deriveTestNlink(t, fixture.layer, fixture.rootID); got != 2 {
		t.Fatalf("parent Nlink after Rmdir = %d, want 2", got)
	}
}

func TestRmdirRejectsNonEmptyDirectory(t *testing.T) {
	fixture := newNamespaceFixture(t)
	directory, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "not-empty"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Create(CreateRequest{ParentID: directory.ObjectID, Name: "child"}); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.layer.Rmdir(RmdirRequest{ParentID: fixture.rootID, Name: "not-empty"}); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("Rmdir non-empty directory = %v, want ENOTEMPTY", err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "not-empty"}); err != nil {
		t.Fatalf("directory should remain after rejected Rmdir: %v", err)
	}
	if fixture.layer.objects[directory.ObjectID].header.Deleted {
		t.Fatal("non-empty directory was marked deleted")
	}
}

func TestRmdirRejectsNonDirectoryAndMissingEntries(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Rmdir(RmdirRequest{ParentID: fixture.rootID, Name: "file"}); !errors.Is(err, errNotDirectory) {
		t.Fatalf("Rmdir regular file = %v, want not-directory", err)
	}
	if _, err := fixture.layer.Rmdir(RmdirRequest{ParentID: fixture.rootID, Name: "missing"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Rmdir missing entry = %v, want not-exist", err)
	}
	if _, err := fixture.layer.Rmdir(RmdirRequest{ParentID: file.ObjectID, Name: "child"}); !errors.Is(err, errNotDirectory) {
		t.Fatalf("Rmdir with non-directory parent = %v, want not-directory", err)
	}
}

func TestRmdirCancellationDoesNotChangeNamespace(t *testing.T) {
	fixture := newNamespaceFixture(t)
	directory, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.layer.Rmdir(RmdirRequest{Context: ctx, ParentID: fixture.rootID, Name: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Rmdir = %v, want context canceled", err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "cancelled"}); err != nil {
		t.Fatalf("directory disappeared after canceled Rmdir: %v", err)
	}
	if fixture.layer.objects[directory.ObjectID].header.Deleted {
		t.Fatal("canceled Rmdir marked directory deleted")
	}
}

func deriveTestNlink(t *testing.T, layer *Orchestration, objectID shared.ObjectID) uint32 {
	t.Helper()
	attributes, err := layer.GetAttr(GetAttrRequest{ObjectID: objectID})
	if err != nil {
		t.Fatal(err)
	}
	return attributes.Attributes.Nlink
}
