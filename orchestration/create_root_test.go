package orchestration

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
)

func newEmptyDistribution(t *testing.T) ([]*physicalio.IO, *distribution.Distribution) {
	t.Helper()
	ios := make([]*physicalio.IO, 4)
	for index := range ios {
		ios[index] = physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer
	}
	result, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	return ios, result.Layer
}

func TestCreateRootCreatesAndRestoresEmptyFilesystem(t *testing.T) {
	ios, distributed := newEmptyDistribution(t)
	created, err := CreateRoot(CreateRootRequest{Distribution: distributed, Mode: 0750, UID: 123, GID: 456})
	if err != nil {
		t.Fatal(err)
	}
	if created.Layer == nil {
		t.Fatal("CreateRoot returned a nil Orchestration layer")
	}
	if _, err := created.RootID.MarshalBinary(); err != nil {
		t.Fatalf("root ObjectID is invalid: %v", err)
	}
	root, err := created.Layer.GetAttr(GetAttrRequest{ObjectID: created.RootID})
	if err != nil {
		t.Fatal(err)
	}
	attributes := root.Attributes
	if attributes.Kind != objectKindDirectory || attributes.Mode != syscall.S_IFDIR|0750 ||
		attributes.UID != 123 || attributes.GID != 456 || attributes.Nlink != 2 || attributes.Size != 0 {
		t.Fatalf("root attributes = %+v", attributes)
	}
	entries, err := created.Layer.ReadDir(ReadDirRequest{ObjectID: created.RootID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries.Entries) != 0 {
		t.Fatalf("new root contains entries: %+v", entries.Entries)
	}
	if got := created.Layer.countReachableObjects(); got != 1 {
		t.Fatalf("reachable object count = %d, want root only", got)
	}
	listed, err := distributed.ListObjects(distribution.ListObjectsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.ObjectIDs) != 1 || listed.ObjectIDs[0] != created.RootID {
		t.Fatalf("persisted ObjectIDs = %v, want only root %s", listed.ObjectIDs, created.RootID)
	}

	restartedDistribution, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(NewRequest{Distribution: restartedDistribution.Layer})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Layer.GetAttr(GetAttrRequest{ObjectID: created.RootID})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Attributes.Kind != objectKindDirectory || restored.Attributes.Mode != syscall.S_IFDIR|0750 ||
		restored.Attributes.UID != 123 || restored.Attributes.GID != 456 || restored.Attributes.Nlink != 2 {
		t.Fatalf("restored root attributes = %+v", restored.Attributes)
	}
	restoredEntries, err := restarted.Layer.ReadDir(ReadDirRequest{ObjectID: created.RootID})
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredEntries.Entries) != 0 || restarted.Layer.countReachableObjects() != 1 {
		t.Fatalf("restored filesystem is not empty: entries=%+v objects=%d", restoredEntries.Entries, restarted.Layer.countReachableObjects())
	}
}

func TestCreateRootRejectsNonEmptyStorage(t *testing.T) {
	_, distributed := newEmptyDistribution(t)
	created, err := CreateRoot(CreateRootRequest{Distribution: distributed, Mode: 0755})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateRoot(CreateRootRequest{Distribution: distributed, Mode: 0755}); err == nil {
		t.Fatal("CreateRoot succeeded on storage that already contains a root")
	}
	if created.Layer.root == nil || created.Layer.root.header.ObjectID != created.RootID {
		t.Fatal("existing filesystem was modified by rejected CreateRoot")
	}
}

func TestCreateRootRejectsInvalidAndCancelledRequests(t *testing.T) {
	if _, err := CreateRoot(CreateRootRequest{}); err == nil {
		t.Fatal("CreateRoot accepted nil Distribution")
	}
	_, distributed := newEmptyDistribution(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateRoot(CreateRootRequest{Context: ctx, Distribution: distributed}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled CreateRoot = %v, want context.Canceled", err)
	}
	objects, err := distributed.ListObjects(distribution.ListObjectsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.ObjectIDs) != 0 {
		t.Fatalf("cancelled CreateRoot persisted objects: %v", objects.ObjectIDs)
	}
}

func TestCreateRootPersistenceFailureDoesNotPublishFilesystem(t *testing.T) {
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
	for _, root := range roots {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root, []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateRoot(CreateRootRequest{Distribution: distributed, Mode: 0755}); err == nil {
		t.Fatal("CreateRoot succeeded despite unavailable storage")
	}
	objects, err := distributed.ListObjects(distribution.ListObjectsRequest{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.ObjectIDs) != 0 {
		t.Fatalf("failed CreateRoot published objects: %v", objects.ObjectIDs)
	}
}

func TestCreateRootMasksFileTypeBitsFromMode(t *testing.T) {
	_, distributed := newEmptyDistribution(t)
	created, err := CreateRoot(CreateRootRequest{Distribution: distributed, Mode: syscall.S_IFREG | 0755})
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := created.Layer.GetAttr(GetAttrRequest{ObjectID: created.RootID})
	if err != nil {
		t.Fatal(err)
	}
	if attributes.Attributes.Mode != syscall.S_IFDIR|0755 {
		t.Fatalf("root Mode = %#o, want %#o", attributes.Attributes.Mode, uint32(syscall.S_IFDIR|0755))
	}
}
