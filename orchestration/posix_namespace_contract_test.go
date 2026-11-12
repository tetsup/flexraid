package orchestration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/tetsup/flexraid/distribution"
)

// POSIX metadata belongs to the backing File and must be identical through
// every Namespace name that refers to it, including after reconstruction.
func TestSetAttrUpdatesAllPOSIXMetadataAcrossHardLinksAndRestart(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{
		ParentID: fixture.rootID, Name: "original", Mode: 0644, UID: 10, GID: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	link, err := fixture.layer.Link(LinkRequest{
		SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "alias",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: file.ObjectID})
	if err != nil {
		t.Fatal(err)
	}

	mode, uid, gid := uint32(0601), uint32(1234), uint32(5678)
	atime := time.Unix(1700000000, 123)
	mtime := time.Unix(1700000100, 456)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{
		ObjectID: link.ObjectID, Mode: &mode, UID: &uid, GID: &gid,
		Atime: &atime, Mtime: &mtime,
	}); err != nil {
		t.Fatal(err)
	}

	for _, objectID := range []interface{ String() string }{file.ObjectID, link.ObjectID} {
		parsed := testID(objectID.String())
		got, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: parsed})
		if err != nil {
			t.Fatalf("GetAttr(%s): %v", objectID.String(), err)
		}
		attr := got.Attributes
		if attr.ObjectID != file.ObjectID || attr.Mode != mode || attr.UID != uid || attr.GID != gid ||
			!attr.Atime.Equal(atime) || !attr.Mtime.Equal(mtime) || !attr.Ctime.After(before.Attributes.Ctime) ||
			attr.Nlink != 2 {
			t.Errorf("attributes through %s = %+v; want shared identity, updated metadata, and NLink 2", objectID.String(), attr)
		}
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restoredLink, err := restarted.Layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Layer.GetAttr(GetAttrRequest{ObjectID: restoredLink.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	attr := restored.Attributes
	if attr.ObjectID != file.ObjectID || attr.Mode != mode || attr.UID != uid || attr.GID != gid ||
		!attr.Atime.Equal(atime) || !attr.Mtime.Equal(mtime) || attr.Nlink != 2 {
		t.Fatalf("restored attributes = %+v; metadata was not persisted consistently", attr)
	}
}

func TestSetAttrTruncateShrinksAndZeroExtendsFileData(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "data", Mode: 0600})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := fixture.layer.Open(OpenRequest{ObjectID: file.ObjectID, Flags: syscall.O_RDWR})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("abcdefghij")
	if written, err := fixture.layer.Write(WriteRequest{
		Handle: opened.Handle, ObjectID: file.ObjectID, Data: payload,
	}); err != nil || written.Written != uint32(len(payload)) {
		t.Fatalf("Write = %+v, %v", written, err)
	}
	if _, err := fixture.layer.Flush(FlushRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Release(ReleaseRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}

	shrunkSize := uint64(4)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: file.ObjectID, Size: &shrunkSize}); err != nil {
		t.Fatal(err)
	}
	expandedSize := uint64(8)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: file.ObjectID, Size: &expandedSize}); err != nil {
		t.Fatal(err)
	}

	reader, err := fixture.layer.Open(OpenRequest{ObjectID: file.ObjectID, Flags: syscall.O_RDONLY})
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.layer.Release(ReleaseRequest{Handle: reader.Handle})
	read, err := fixture.layer.Read(ReadRequest{
		Handle: reader.Handle, ObjectID: file.ObjectID, Offset: 0, Size: uint32(expandedSize),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'a', 'b', 'c', 'd', 0, 0, 0, 0}
	if !bytes.Equal(read.Data, want) {
		t.Fatalf("data after shrink then extend = %v, want %v", read.Data, want)
	}
	attributes, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: file.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if attributes.Attributes.Size != expandedSize {
		t.Fatalf("GetAttr size = %d, want %d", attributes.Attributes.Size, expandedSize)
	}
}

func TestRenameDirectoryBetweenParentsUpdatesBothParentNlinks(t *testing.T) {
	fixture := newNamespaceFixture(t)
	sourceParent, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "source"})
	if err != nil {
		t.Fatal(err)
	}
	destinationParent, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := fixture.layer.Mkdir(MkdirRequest{ParentID: sourceParent.ObjectID, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}

	if got := deriveTestNlink(t, fixture.layer, sourceParent.ObjectID); got != 3 {
		t.Fatalf("source parent NLink before Rename = %d, want 3", got)
	}
	if got := deriveTestNlink(t, fixture.layer, destinationParent.ObjectID); got != 2 {
		t.Fatalf("destination parent NLink before Rename = %d, want 2", got)
	}

	if _, err := fixture.layer.Rename(RenameRequest{
		SourceParentID: sourceParent.ObjectID, SourceName: "child",
		DestinationParentID: destinationParent.ObjectID, DestinationName: "moved",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: sourceParent.ObjectID, Name: "child"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old directory name lookup = %v, want not-exist", err)
	}
	entry, err := fixture.layer.Lookup(LookupRequest{ParentID: destinationParent.ObjectID, Name: "moved"})
	if err != nil {
		t.Fatal(err)
	}
	attr, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: entry.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if attr.Attributes.ObjectID != moved.ObjectID || attr.Attributes.Kind != objectKindDirectory || attr.Attributes.Nlink != 2 {
		t.Fatalf("moved directory attributes = %+v; want same directory identity and NLink 2", attr.Attributes)
	}
	if got := deriveTestNlink(t, fixture.layer, sourceParent.ObjectID); got != 2 {
		t.Fatalf("source parent NLink after Rename = %d, want 2", got)
	}
	if got := deriveTestNlink(t, fixture.layer, destinationParent.ObjectID); got != 3 {
		t.Fatalf("destination parent NLink after Rename = %d, want 3", got)
	}
}

func TestUnlinkRegularFileAndSymlinkRemovesOnlyTheirNames(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "file"})
	if err != nil {
		t.Fatal(err)
	}
	link, err := fixture.layer.Symlink(SymlinkRequest{
		ParentID: fixture.rootID, Name: "symlink", Target: "target",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"file", "symlink"} {
		if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: name}); err != nil {
			t.Fatalf("Unlink(%q): %v", name, err)
		}
		if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: name}); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Lookup(%q) after Unlink = %v, want not-exist", name, err)
		}
	}
	for _, objectID := range []interface{ String() string }{file.ObjectID, link.ObjectID} {
		if _, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: testID(objectID.String())}); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("GetAttr(%s) after Unlink = %v, want not-exist", objectID.String(), err)
		}
	}
	if got := deriveTestNlink(t, fixture.layer, fixture.rootID); got != 2 {
		t.Fatalf("root NLink after file and symlink Unlink = %d, want 2", got)
	}
}
