package orchestration

import (
	"errors"
	"os"
	"testing"

	"github.com/tetsup/flexraid/distribution"
)

func TestUnlinkFileWithHardLinkDetachesParentOnly(t *testing.T) {
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
	attr, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: alias.ObjectID})
	if err != nil {
		t.Fatalf("alias should still resolve to backing: %v", err)
	}
	if attr.Attributes.Nlink != 1 {
		t.Fatalf("remaining alias NLink = %d, want 1", attr.Attributes.Nlink)
	}
	if fixture.layer.objects[file.ObjectID].header.Kind != objectKindFile {
		t.Fatal("unlink changed the data object's Kind")
	}
	if _, err := fixture.layer.Unlink(UnlinkRequest{ParentID: fixture.rootID, Name: "a"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repeat unlink = %v, want os.ErrNotExist", err)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restoredAlias, err := restarted.Layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "b"})
	if err != nil || restoredAlias.Kind != objectKindHardLink {
		t.Fatalf("restored alias = %+v, %v", restoredAlias, err)
	}
	restoredAttr, err := restarted.Layer.GetAttr(GetAttrRequest{ObjectID: restoredAlias.ObjectID})
	if err != nil || restoredAttr.Attributes.Nlink != 1 {
		t.Fatalf("restored alias attributes = %+v, %v; want NLink 1", restoredAttr, err)
	}
}

func TestUnlinkDetachedFileThroughHardLinkLifecycle(t *testing.T) {
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

func TestUnlinkLastHardLinkKeepsDirectFileKind(t *testing.T) {
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
