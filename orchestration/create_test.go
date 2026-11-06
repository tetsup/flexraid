package orchestration

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/tetsup/flexraid/distribution"
)

func TestCreatePersistsAndPublishesFileWithDerivedNlink(t *testing.T) {
	fixture := newNamespaceFixture(t)
	rootNlink := func() uint32 {
		t.Helper()
		attr, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: fixture.rootID})
		if err != nil {
			t.Fatal(err)
		}
		return attr.Attributes.Nlink
	}
	if got := rootNlink(); got != 2 {
		t.Fatalf("root Nlink before Create = %d, want 2", got)
	}

	created, err := fixture.layer.Create(CreateRequest{
		ParentID: fixture.rootID, Name: "notes.txt", Mode: 0640, UID: 42, GID: 84,
	})
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "notes.txt"})
	if err != nil || lookup.ObjectID != created.ObjectID || lookup.Kind != objectKindFile {
		t.Fatalf("Lookup = %+v, %v", lookup, err)
	}
	attributes, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: created.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if got := attributes.Attributes; got.Kind != objectKindFile || got.Nlink != 1 || got.Mode != 0640 || got.UID != 42 || got.GID != 84 || got.Size != 0 {
		t.Fatalf("created file attributes = %+v", got)
	}
	if got := rootNlink(); got != 2 {
		t.Fatalf("root Nlink after file Create = %d, want unchanged 2", got)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "notes.txt"})
	if err != nil || restored.ObjectID != created.ObjectID {
		t.Fatalf("Lookup after restart = %+v, %v", restored, err)
	}
	restoredAttr, err := restarted.Layer.GetAttr(GetAttrRequest{ObjectID: created.ObjectID})
	if err != nil || restoredAttr.Attributes.Nlink != 1 {
		t.Fatalf("GetAttr after restart = %+v, %v", restoredAttr, err)
	}
}

func TestCreateRejectsInvalidRequestsWithoutPublishing(t *testing.T) {
	fixture := newNamespaceFixture(t)
	if _, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "existing", Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "a/b", "bad\x00name"} {
		t.Run("invalid-name-"+name, func(t *testing.T) {
			if _, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: name}); err == nil {
				t.Fatalf("Create accepted invalid name %q", name)
			}
		})
	}
	if _, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "existing"}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate Create = %v, want os.ErrExist", err)
	}
	missing := testID("22345678-9abc-4def-8abc-1234567890ab")
	if _, err := fixture.layer.Create(CreateRequest{ParentID: missing, Name: "missing-parent"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Create under missing parent = %v, want os.ErrNotExist", err)
	}
	link, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "not-directory", Target: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Create(CreateRequest{ParentID: link.ObjectID, Name: "child"}); !errors.Is(err, errNotDirectory) {
		t.Fatalf("Create under non-directory = %v, want errNotDirectory", err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "missing-parent"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Create was published: %v", err)
	}
}

func TestCreateCancellationAndPersistenceFailureDoNotPublish(t *testing.T) {
	fixture := newNamespaceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.layer.Create(CreateRequest{Context: ctx, ParentID: fixture.rootID, Name: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Create = %v, want context.Canceled", err)
	}
	if len(fixture.layer.objects) != 1 || len(fixture.layer.root.children) != 0 {
		t.Fatal("canceled Create changed namespace")
	}

	for _, root := range fixture.roots {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root, []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.layer.Create(CreateRequest{ParentID: fixture.rootID, Name: "failed-persist"}); err == nil {
		t.Fatal("Create succeeded despite unavailable storage")
	}
	if len(fixture.layer.objects) != 1 || len(fixture.layer.root.children) != 0 {
		t.Fatal("failed Create changed namespace")
	}
}
