package orchestration

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
)

func TestRenamePreservesUpdatedFileHeader(t *testing.T) {
	ios, distributed := newEmptyDistribution(t)
	root, err := CreateRoot(CreateRootRequest{Distribution: distributed, Mode: 0755})
	if err != nil {
		t.Fatal(err)
	}
	created, err := root.Layer.Create(CreateRequest{
		ParentID: root.RootID, Name: "before", Mode: 0644,
	})
	if err != nil {
		t.Fatal(err)
	}

	opened, err := root.Layer.Open(OpenRequest{
		ObjectID: created.ObjectID, Flags: uint32(syscall.O_RDWR),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = root.Layer.Release(ReleaseRequest{Handle: opened.Handle})
	}()

	payload := []byte("updated file data")
	if written, err := root.Layer.Write(WriteRequest{
		Handle: opened.Handle, ObjectID: created.ObjectID, Data: payload,
	}); err != nil || written.Written != uint32(len(payload)) {
		t.Fatalf("Write = %+v, %v", written, err)
	}
	mode, uid := uint32(0600), uint32(1234)
	if _, err := root.Layer.SetAttr(SetAttrRequest{
		ObjectID: created.ObjectID, Handle: opened.Handle, Mode: &mode, UID: &uid,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = root.Layer.Rename(RenameRequest{
		SourceParentID: root.RootID, SourceName: "before",
		DestinationParentID: root.RootID, DestinationName: "after",
	})
	if err != nil {
		t.Fatal(err)
	}

	destination, err := root.Layer.Lookup(LookupRequest{ParentID: root.RootID, Name: "after"})
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := root.Layer.GetAttr(GetAttrRequest{ObjectID: destination.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if got := attributes.Attributes; got.Size != uint64(len(payload)) || got.Mode != mode || got.UID != uid {
		t.Fatalf("attributes after Rename = %+v, want size=%d mode=%#o uid=%d",
			got, len(payload), mode, uid)
	}

	if _, err := root.Layer.Flush(FlushRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := restarted.Layer.Lookup(LookupRequest{ParentID: root.RootID, Name: "after"})
	if err != nil || lookup.Kind != objectKindHardLink {
		t.Fatalf("Lookup after restart = %+v, %v; want destination HardLink", lookup, err)
	}
	restoredAttributes, err := restarted.Layer.GetAttr(GetAttrRequest{ObjectID: lookup.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if got := restoredAttributes.Attributes; got.Size != uint64(len(payload)) || got.Mode != mode || got.UID != uid {
		t.Fatalf("restored attributes = %+v, want size=%d mode=%#o uid=%d",
			got, len(payload), mode, uid)
	}

	reader, err := restarted.Layer.Open(OpenRequest{
		ObjectID: lookup.ObjectID, Flags: uint32(syscall.O_RDONLY),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = restarted.Layer.Release(ReleaseRequest{Handle: reader.Handle})
	}()
	read, err := restarted.Layer.Read(ReadRequest{
		Handle: reader.Handle, ObjectID: lookup.ObjectID,
		Offset: 0, Size: uint32(len(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read.Data, payload) {
		t.Fatalf("data after Rename and restart = %q, want %q", read.Data, payload)
	}
}

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
	if err != nil || got.Kind != objectKindHardLink {
		t.Fatalf("destination lookup = %+v, %v; want HardLink", got, err)
	}
	object := fixture.layer.objects[file.ObjectID]
	if object.header.ParentID != nil || object.header.Deleted {
		t.Fatalf("source backing after rename = %+v; want detached, live File", object.header)
	}
	destinationObject := fixture.layer.objects[got.ObjectID]
	if destinationObject == nil || destinationObject.header.TargetID == nil {
		t.Fatalf("destination HardLink object = %+v, want a TargetID", destinationObject)
	}
	if target := object.header.ObjectID; *destinationObject.header.TargetID != target {
		t.Fatalf("destination HardLink target = %s, want backing %s", *destinationObject.header.TargetID, target)
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
	if err != nil || got.Kind != objectKindHardLink {
		t.Fatalf("destination lookup = %+v, %v; want HardLink", got, err)
	}
	linkObject := fixture.layer.objects[got.ObjectID]
	if linkObject == nil || linkObject.header.TargetID == nil || *linkObject.header.TargetID != source.ObjectID {
		t.Fatalf("destination HardLink = %+v; want target %s", linkObject, source.ObjectID)
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
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("directory cycle rename = %v, want EINVAL", err)
	}

	_, err = fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "file"})
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
