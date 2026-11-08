package orchestration

import (
	"context"
	"errors"
	"os"
	"syscall"
	"strings"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
)

type namespaceFixture struct {
	layer  *Orchestration
	ios    []*physicalio.IO
	roots  []string
	rootID shared.ObjectID
}

func newNamespaceFixture(t *testing.T) namespaceFixture {
	t.Helper()
	roots := make([]string, 4)
	ios := make([]*physicalio.IO, 4)
	for index := range ios {
		roots[index] = t.TempDir()
		ios[index] = physicalio.New(physicalio.NewRequest{Root: roots[index]}).Layer
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	rootHeader := testHeader(objectKindDirectory, rootID, nil, nil, "")
	rootHeader.Mode, rootHeader.UID, rootHeader.GID, rootHeader.Size = 0755, 100, 200, 0
	layer := &Orchestration{distribution: distributed.Layer}
	rootHeader, err = layer.persistNewObject(context.Background(), rootHeader, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := &runtimeObject{header: rootHeader, children: make(map[string]*runtimeObject)}
	layer.objects = map[shared.ObjectID]*runtimeObject{rootID: root}
	layer.root = root
	layer.headers = map[shared.ObjectID]header{rootID: rootHeader}
	return namespaceFixture{layer: layer, ios: ios, roots: roots, rootID: rootID}
}

func TestMkdirPersistsAndPublishesDirectory(t *testing.T) {
	fixture := newNamespaceFixture(t)
	created, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "documents", Mode: 0750, UID: 123, GID: 456})
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "documents"})
	if err != nil || lookup.ObjectID != created.ObjectID || lookup.Kind != objectKindDirectory {
		t.Fatalf("Lookup = %+v, %v", lookup, err)
	}
	attributes, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: created.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if got := attributes.Attributes; got.Mode != 0750 || got.UID != 123 || got.GID != 456 || got.Nlink != 2 || got.Size != 0 {
		t.Fatalf("directory attributes = %+v", got)
	}
	entries, err := fixture.layer.ReadDir(ReadDirRequest{ObjectID: fixture.rootID})
	if err != nil || len(entries.Entries) != 1 || entries.Entries[0].ObjectID != created.ObjectID {
		t.Fatalf("ReadDir = %+v, %v", entries, err)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "documents"})
	if err != nil || restored.ObjectID != created.ObjectID {
		t.Fatalf("Lookup after restart = %+v, %v", restored, err)
	}
}

func TestSymlinkPersistsTargetAcrossStripesAndRestart(t *testing.T) {
	fixture := newNamespaceFixture(t)
	target := "/" + strings.Repeat("nested/", 2500) + "destination"
	created, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "shortcut", Target: target, UID: 321, GID: 654})
	if err != nil {
		t.Fatal(err)
	}
	readLink, err := fixture.layer.ReadLink(ReadLinkRequest{ObjectID: created.ObjectID})
	if err != nil || readLink.Target != target {
		t.Fatalf("ReadLink = %d bytes, %v", len(readLink.Target), err)
	}
	attributes, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: created.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	if got := attributes.Attributes; got.Kind != objectKindSymlink || got.Mode != 0777 || got.UID != 321 || got.GID != 654 || got.Size != uint64(len(target)) {
		t.Fatalf("symlink attributes = %+v", got)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: fixture.ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Layer.ReadLink(ReadLinkRequest{ObjectID: created.ObjectID})
	if err != nil || restored.Target != target {
		t.Fatalf("ReadLink after restart = %d bytes, %v", len(restored.Target), err)
	}
}

func TestMkdirAndSymlinkRejectInvalidRequests(t *testing.T) {
	fixture := newNamespaceFixture(t)
	_, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "existing", Mode: 0755})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "a/b", "bad\x00name"} {
		t.Run("invalid-name-"+name, func(t *testing.T) {
			if _, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: name}); err == nil {
				t.Fatalf("Mkdir accepted name %q", name)
			}
		})
	}
	if _, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "existing"}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate Mkdir = %v, want os.ErrExist", err)
	}
	if _, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "existing", Target: "target"}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate Symlink = %v, want os.ErrExist", err)
	}
	if _, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "empty", Target: ""}); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("empty Symlink target = %v, want ENOENT", err)
	}
	nonDirectory, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "not-directory", Target: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Mkdir(MkdirRequest{ParentID: nonDirectory.ObjectID, Name: "child"}); !errors.Is(err, errNotDirectory) {
		t.Fatalf("Mkdir under non-directory = %v, want errNotDirectory", err)
	}
	missing := testID("22345678-9abc-4def-8abc-1234567890ab")
	if _, err := fixture.layer.Symlink(SymlinkRequest{ParentID: missing, Name: "link", Target: "target"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Symlink under missing parent = %v, want os.ErrNotExist", err)
	}
	if _, err := fixture.layer.Lookup(LookupRequest{ParentID: fixture.rootID, Name: "empty"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Symlink was published: %v", err)
	}
}

func TestMkdirAndSymlinkCancellationDoesNotPublish(t *testing.T) {
	fixture := newNamespaceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.layer.Mkdir(MkdirRequest{Context: ctx, ParentID: fixture.rootID, Name: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Mkdir = %v", err)
	}
	if _, err := fixture.layer.Symlink(SymlinkRequest{Context: ctx, ParentID: fixture.rootID, Name: "cancelled-link", Target: "target"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Symlink = %v", err)
	}
	if len(fixture.layer.objects) != 1 || len(fixture.layer.root.children) != 0 {
		t.Fatal("canceled creation changed namespace state")
	}
}

func TestMkdirPersistenceFailureDoesNotPublish(t *testing.T) {
	fixture := newNamespaceFixture(t)
	for _, root := range fixture.roots {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root, []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "not-published"}); err == nil {
		t.Fatal("Mkdir succeeded despite unavailable storage")
	}
	if _, exists := fixture.layer.root.children["not-published"]; exists {
		t.Fatal("failed Mkdir was published in parent")
	}
	if len(fixture.layer.objects) != 1 {
		t.Fatalf("objects = %d, want only root", len(fixture.layer.objects))
	}
}

func TestNamespaceCreationDerivesNlinkFromCurrentChildren(t *testing.T) {
	fixture := newNamespaceFixture(t)
	getNlink := func(id shared.ObjectID) uint32 {
		t.Helper()
		attr, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: id})
		if err != nil {
			t.Fatalf("GetAttr(%s): %v", id, err)
		}
		return attr.Attributes.Nlink
	}

	if got := getNlink(fixture.rootID); got != 2 {
		t.Fatalf("empty root Nlink = %d, want 2", got)
	}
	first, err := fixture.layer.Mkdir(MkdirRequest{ParentID: fixture.rootID, Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if got := getNlink(fixture.rootID); got != 3 {
		t.Fatalf("root Nlink after child Directory = %d, want 3", got)
	}
	if got := getNlink(first.ObjectID); got != 2 {
		t.Fatalf("new Directory Nlink = %d, want 2", got)
	}

	_, err = fixture.layer.Mkdir(MkdirRequest{ParentID: first.ObjectID, Name: "nested"})
	if err != nil {
		t.Fatal(err)
	}
	if got := getNlink(fixture.rootID); got != 3 {
		t.Fatalf("root Nlink after nested Directory = %d, want 3", got)
	}
	if got := getNlink(first.ObjectID); got != 3 {
		t.Fatalf("parent Directory Nlink after child Directory = %d, want 3", got)
	}

	if _, err := fixture.layer.Symlink(SymlinkRequest{ParentID: fixture.rootID, Name: "link", Target: "target"}); err != nil {
		t.Fatal(err)
	}
	if got := getNlink(fixture.rootID); got != 3 {
		t.Fatalf("root Nlink after Symlink = %d, want 3", got)
	}
}
