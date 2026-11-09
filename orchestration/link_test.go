package orchestration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	"github.com/tetsup/flexraid/pkg/shared"
)

func TestLinkKeepsFileKindAndSharesData(t *testing.T) {
	fixture := newNamespaceFixture(t)
	created, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "original", Mode: 0640})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := fixture.layer.Open(OpenRequest{ObjectID: created.ObjectID, Flags: syscall.O_RDWR})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("shared file contents")
	if _, err := fixture.layer.Write(WriteRequest{Handle: opened.Handle, ObjectID: created.ObjectID, Data: content}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Flush(FlushRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}

	linked, err := fixture.layer.Link(LinkRequest{SourceID: created.ObjectID, ParentID: fixture.rootID, Name: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	if linked.ObjectID == created.ObjectID {
		t.Fatal("new HardLink reused the backing ObjectID")
	}
	original, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "original"})
	if err != nil || original.ObjectID != created.ObjectID || original.Kind != objectKindFile {
		t.Fatalf("original lookup = %+v, %v", original, err)
	}
	alias, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "alias"})
	if err != nil || alias.ObjectID != linked.ObjectID || alias.Kind != objectKindHardLink {
		t.Fatalf("alias lookup = %+v, %v", alias, err)
	}
	if got := fixture.layer.objects[created.ObjectID].header.ParentID; got == nil || *got != fixture.rootID {
		t.Fatalf("File ParentID = %v, want root %s", got, fixture.rootID)
	}
	for _, id := range []string{created.ObjectID.String(), linked.ObjectID.String()} {
		parsed := testID(id)
		attr, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: parsed})
		if err != nil || attr.Attributes.Nlink != 2 {
			t.Fatalf("GetAttr(%s) = %+v, %v; want NLink 2", id, attr, err)
		}
	}

	aliasHandle, err := fixture.layer.Open(OpenRequest{ObjectID: linked.ObjectID, Flags: syscall.O_RDONLY})
	if err != nil {
		t.Fatal(err)
	}
	read, err := fixture.layer.Read(ReadRequest{Handle: aliasHandle.Handle, ObjectID: linked.ObjectID, Size: uint32(len(content))})
	if err != nil || !bytes.Equal(read.Data, content) {
		t.Fatalf("read through alias = %q, %v", read.Data, err)
	}
	if _, err := fixture.layer.Release(ReleaseRequest{Handle: aliasHandle.Handle}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Release(ReleaseRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restoredOriginal, err := restarted.Layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "original"})
	if err != nil || restoredOriginal.Kind != objectKindFile {
		t.Fatalf("restored original = %+v, %v", restoredOriginal, err)
	}
	restoredAlias, err := restarted.Layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "alias"})
	if err != nil || restoredAlias.Kind != objectKindHardLink {
		t.Fatalf("restored alias = %+v, %v", restoredAlias, err)
	}
	handle, err := restarted.Layer.Open(OpenRequest{ObjectID: restoredAlias.ObjectID, Flags: syscall.O_RDONLY})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Layer.Release(ReleaseRequest{Handle: handle.Handle})
	read, err = restarted.Layer.Read(ReadRequest{Handle: handle.Handle, ObjectID: restoredAlias.ObjectID, Size: uint32(len(content))})
	if err != nil || !bytes.Equal(read.Data, content) {
		t.Fatalf("read after restart = %q, %v", read.Data, err)
	}
}

func TestLinkFromHardLinkAddsAnotherDirectReference(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.layer.Link(LinkRequest{SourceID: first.ObjectID, ParentID: fixture.rootID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fixture.layer.objects[second.ObjectID].header.TargetID; got == nil || *got != file.ObjectID {
		t.Fatalf("second HardLink target = %v, want backing %s", got, file.ObjectID)
	}
	for _, name := range []string{"a", "b", "c"} {
		entry, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: name})
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		attr, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: entry.ObjectID})
		if err != nil || attr.Attributes.Nlink != 3 {
			t.Fatalf("GetAttr(%q) = %+v, %v; want NLink 3", name, attr, err)
		}
	}
}

func TestLinkRejectsInvalidRequestsWithoutAddingName(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "file"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "bad/name", "bad\x00name"} {
		if _, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: name}); err == nil {
			t.Errorf("Link accepted invalid name %q", name)
		}
	}
	if _, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "alias"}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate Link = %v, want os.ErrExist", err)
	}
	if _, err := fixture.layer.Link(LinkRequest{SourceID: fixture.rootID, ParentID: fixture.rootID, Name: "directory"}); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("Link directory = %v, want EPERM", err)
	}
	missing := testID("22345678-9abc-4def-8abc-1234567890ab")
	if _, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: missing, Name: "missing-parent"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Link under missing parent = %v, want os.ErrNotExist", err)
	}
	symlink, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "sym", Target: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Link(LinkRequest{SourceID: symlink.ObjectID, ParentID: fixture.rootID, Name: "sym-link"}); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("Link symlink = %v, want EPERM", err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "missing-parent"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Link published a name: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.layer.Link(LinkRequest{Context: ctx, SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Link = %v, want context.Canceled", err)
	}
}

func TestGetAttrHidesHardLinkAndReturnsBackingIdentity(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "original", Mode: 0640})
	if err != nil {
		t.Fatal(err)
	}
	link, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []shared.ObjectID{file.ObjectID, link.ObjectID} {
		got, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: id})
		if err != nil {
			t.Fatalf("GetAttr(%s): %v", id, err)
		}
		if got.Attributes.ObjectID != file.ObjectID || got.Attributes.Kind != objectKindFile {
			t.Fatalf("GetAttr(%s) identity/kind = %s/%v, want backing %s/File", id, got.Attributes.ObjectID, got.Attributes.Kind, file.ObjectID)
		}
		if got.Attributes.Nlink != 2 || got.Attributes.Mode != 0640 {
			t.Fatalf("GetAttr(%s) attrs = %+v, want nlink 2 and mode 0640", id, got.Attributes)
		}
	}
}
