package orchestration

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestRenameMovesEntryBetweenDirectories(t *testing.T) {
	fixture := newNamespaceFixture(t)
	sourceDir, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "source"})
	if err != nil {
		t.Fatal(err)
	}
	destinationDir, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := fixture.layer.Create(CreateRequest{ParentID: sourceDir.ObjectID, Name: "old"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.layer.Rename(RenameRequest{
		SourceParentID: sourceDir.ObjectID, SourceName: "old",
		DestinationParentID: destinationDir.ObjectID, DestinationName: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: sourceDir.ObjectID, Name: "old"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old name lookup = %v, want not-exist", err)
	}
	got, err := fixture.layer.Lookup(LookupRequest{ParentID: destinationDir.ObjectID, Name: "new"})
	if err != nil || got.ObjectID != file.ObjectID {
		t.Fatalf("destination lookup = %+v, %v", got, err)
	}
	object := fixture.layer.objects[file.ObjectID]
	if object.header.ParentID == nil || *object.header.ParentID != destinationDir.ObjectID || object.header.Name != "new" {
		t.Fatalf("renamed header = %+v", object.header)
	}
}

func TestRenameReplacesRegularFile(t *testing.T) {
	fixture := newNamespaceFixture(t)
	source, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "source"})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.layer.Rename(RenameRequest{
		SourceParentID: fixture.rootID, SourceName: "source",
		DestinationParentID: fixture.rootID, DestinationName: "destination",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "source"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source lookup = %v, want not-exist", err)
	}
	got, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "destination"})
	if err != nil || got.ObjectID != source.ObjectID {
		t.Fatalf("destination lookup = %+v, %v", got, err)
	}
	if !fixture.layer.objects[destination.ObjectID].header.Deleted {
		t.Fatal("replaced destination was not logically deleted")
	}
}

func TestRenameRejectsDirectoryCycleAndIncompatibleReplacement(t *testing.T) {
	fixture := newNamespaceFixture(t)
	dir, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "dir"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := fixture.layer.Mkdir(MkdirRequest{ParentID: dir.ObjectID, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.layer.Rename(RenameRequest{
		SourceParentID: fixture.rootID, SourceName: "dir",
		DestinationParentID: child.ObjectID, DestinationName: "moved",
	})
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("directory cycle rename = %v, want invalid", err)
	}

	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "file"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.layer.Rename(RenameRequest{
		SourceParentID: fixture.rootID, SourceName: "file",
		DestinationParentID: fixture.rootID, DestinationName: "dir",
	})
	if !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("file over directory = %v, want EISDIR", err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "file"}); err != nil {
		t.Fatalf("source should remain after rejected rename: %v", err)
	}
}
