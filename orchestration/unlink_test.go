package orchestration

import (
	"errors"
	"os"
	"testing"
)

func TestUnlinkHardLinkedFileClearsParentOnly(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	alias, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	backing := fixture.layer.objects[file.ObjectID]
	if backing.header.ParentID != nil || backing.header.Deleted {
		t.Fatalf("backing after unlink = %+v; want detached and not deleted", backing.header)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "a"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lookup removed direct name = %v", err)
	}
	if _, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: alias.ObjectID}); err != nil {
		t.Fatalf("alias should still resolve to backing: %v", err)
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "a"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repeat unlink = %v, want os.ErrNotExist", err)
	}
}

func TestUnlinkDetachedHardLinkedFileReturnsError(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	alias, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "b"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unlinked alias no longer exists: %v", err)
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "b"}); err == nil {
		t.Fatal("expected missing alias error")
	}
	_ = alias
}

func TestUnlinkLastHardLinkPromotesDirectFile(t *testing.T) {
	fixture := newNamespaceFixture(t)
	file, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	alias, err := fixture.layer.Link(LinkRequest{SourceID: file.ObjectID, ParentID: fixture.rootID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "b"}); err != nil {
		t.Fatal(err)
	}
	backing := fixture.layer.objects[file.ObjectID]
	if backing.header.Kind != objectKindFile || backing.header.ParentID == nil || backing.header.Deleted {
		t.Fatalf("backing after final alias unlink = %+v; want File with direct parent", backing.header)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "a"}); err != nil {
		t.Fatalf("original file name was not preserved: %v", err)
	}
	if _, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: alias.ObjectID}); err == nil {
		t.Fatal("deleted HardLink still accessible")
	}
}
