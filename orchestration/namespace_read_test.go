package orchestration

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
)

func initializedReadLayer(t *testing.T) (*Orchestration, shared.ObjectID, shared.ObjectID, shared.ObjectID) {
	t.Helper()
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	dirID := testID("22345678-9abc-4def-8abc-1234567890ab")
	fileID := testID("32345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	dir := testHeader(objectKindDirectory, dirID, &rootID, nil, "docs")
	file := testHeader(objectKindFile, fileID, &dirID, nil, "readme")
	file.Mode, file.UID, file.GID, file.Size = 0644, 100, 200, 123
	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{encodeInitial(t, file), encodeInitial(t, root), encodeInitial(t, dir)}); err != nil {
		t.Fatal(err)
	}
	return layer, rootID, dirID, fileID
}

func TestLookup(t *testing.T) {
	layer, rootID, dirID, fileID := initializedReadLayer(t)
	got, err := layer.Lookup(LookupRequest{ParentID: rootID, Name: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ObjectID != dirID || got.Kind != objectKindDirectory {
		t.Fatalf("unexpected lookup result: %#v", got)
	}
	got, err = layer.Lookup(LookupRequest{ParentID: dirID, Name: "readme"})
	if err != nil || got.ObjectID != fileID {
		t.Fatalf("lookup file: result=%#v err=%v", got, err)
	}
	if _, err := layer.Lookup(LookupRequest{ParentID: rootID, Name: "missing"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want os.ErrNotExist", err)
	}
	if _, err := layer.Lookup(LookupRequest{ParentID: fileID, Name: "child"}); !errors.Is(err, errNotDirectory) {
		t.Fatalf("got %v, want errNotDirectory", err)
	}
	if _, err := layer.Lookup(LookupRequest{ParentID: rootID, Name: "../docs"}); err == nil {
		t.Fatal("invalid child name accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := layer.Lookup(LookupRequest{Context: ctx, ParentID: rootID, Name: "docs"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestReadDir(t *testing.T) {
	layer, rootID, dirID, fileID := initializedReadLayer(t)
	got, err := layer.ReadDir(ReadDirRequest{ObjectID: rootID})
	if err != nil {
		t.Fatal(err)
	}
	want := []DirectoryEntry{{ObjectID: dirID, Kind: objectKindDirectory, Name: "docs"}}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("got %#v, want %#v", got.Entries, want)
	}
	got, err = layer.ReadDir(ReadDirRequest{ObjectID: dirID})
	if err != nil {
		t.Fatal(err)
	}
	want = []DirectoryEntry{{ObjectID: fileID, Kind: objectKindFile, Name: "readme"}}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("got %#v, want %#v", got.Entries, want)
	}
	if _, err := layer.ReadDir(ReadDirRequest{ObjectID: fileID}); !errors.Is(err, errNotDirectory) {
		t.Fatalf("got %v, want errNotDirectory", err)
	}
}

func TestGetAttr(t *testing.T) {
	layer, _, _, fileID := initializedReadLayer(t)
	got, err := layer.GetAttr(GetAttrRequest{ObjectID: fileID})
	if err != nil {
		t.Fatal(err)
	}
	attr := got.Attributes
	if attr.ObjectID != fileID || attr.Kind != objectKindFile || attr.Name != "readme" || attr.Mode != 0644 || attr.UID != 100 || attr.GID != 200 || attr.Nlink != 1 || attr.Size != 123 {
		t.Fatalf("unexpected attributes: %#v", attr)
	}
	missing := testID("42345678-9abc-4def-8abc-1234567890ab")
	if _, err := layer.GetAttr(GetAttrRequest{ObjectID: missing}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want os.ErrNotExist", err)
	}
}

func TestResolvePath(t *testing.T) {
	layer, rootID, dirID, fileID := initializedReadLayer(t)
	for input, expected := range map[string]shared.ObjectID{"/": rootID, "/docs": dirID, "/docs/readme": fileID, "/docs/./readme": fileID, "/docs/../docs/readme": fileID} {
		got, err := layer.resolveObjectID(input)
		if err != nil || got != expected {
			t.Errorf("resolveObjectID(%q) = %s, %v; want %s", input, got.String(), err, expected.String())
		}
	}
	if _, err := layer.resolvePath("docs/readme"); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := layer.resolvePath("/missing"); !errors.Is(err, errPathNotFound) {
		t.Fatalf("got %v, want errPathNotFound", err)
	}
	if _, err := layer.resolvePath("/docs/readme/child"); !errors.Is(err, errNotDirectory) {
		t.Fatalf("got %v, want errNotDirectory", err)
	}
}

func TestReadLinkReturnsCachedTargetAndSymlinkAttributes(t *testing.T) {
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	linkID := testID("22345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	link := testHeader(objectKindSymlink, linkID, &rootID, nil, "shortcut")
	target := "../target/file"
	link.Size = uint64(len(target))
	headerBytes, err := link.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, int(cfg.HeaderReservedSize)+len(target))
	copy(data, headerBytes)
	copy(data[int(cfg.HeaderReservedSize):], target)

	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{
		{objectID: rootID, data: mustMarshalHeader(t, root)},
		{objectID: linkID, data: data},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := layer.ReadLink(ReadLinkRequest{ObjectID: linkID})
	if err != nil || got.Target != target {
		t.Fatalf("ReadLink = %#v, %v; want target %q", got, err, target)
	}
	attr, err := layer.GetAttr(GetAttrRequest{ObjectID: linkID})
	if err != nil {
		t.Fatal(err)
	}
	if attr.Attributes.Kind != objectKindSymlink || attr.Attributes.Size != uint64(len(target)) {
		t.Fatalf("GetAttr did not return symlink attributes: %#v", attr.Attributes)
	}
	if _, err := layer.ReadLink(ReadLinkRequest{ObjectID: rootID}); err == nil {
		t.Fatal("ReadLink accepted a non-symlink")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := layer.ReadLink(ReadLinkRequest{Context: ctx, ObjectID: linkID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadLink canceled context: got %v, want context.Canceled", err)
	}
}

func TestInitializeSkipsSymlinkWithUnavailableTarget(t *testing.T) {
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	linkID := testID("22345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	link := testHeader(objectKindSymlink, linkID, &rootID, nil, "broken")
	link.Size = 12
	data := mustMarshalHeader(t, link)

	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{
		{objectID: rootID, data: mustMarshalHeader(t, root)},
		{objectID: linkID, data: data},
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := layer.root.children["broken"]; exists {
		t.Fatal("symlink with unavailable target was exposed in namespace")
	}
	if _, err := layer.ReadLink(ReadLinkRequest{ObjectID: linkID}); err == nil {
		t.Fatal("ReadLink returned an unavailable target")
	}
	if _, protected := layer.protectedObjects[linkID]; !protected {
		t.Fatal("symlink with unavailable target was not protected")
	}
}

func mustMarshalHeader(t *testing.T, h header) []byte {
	t.Helper()
	data, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGetAttrDerivesDirectoryAndHardLinkCounts(t *testing.T) {
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	childDirID := testID("22345678-9abc-4def-8abc-1234567890ab")
	nestedDirID := testID("32345678-9abc-4def-8abc-1234567890ab")
	backingID := testID("42345678-9abc-4def-8abc-1234567890ab")
	linkAID := testID("52345678-9abc-4def-8abc-1234567890ab")
	linkBID := testID("62345678-9abc-4def-8abc-1234567890ab")

	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	childDir := testHeader(objectKindDirectory, childDirID, &rootID, nil, "docs")
	nestedDir := testHeader(objectKindDirectory, nestedDirID, &childDirID, nil, "nested")
	backing := testHeader(objectKindHardLinkedFile, backingID, nil, nil, "")
	linkA := testHeader(objectKindHardLink, linkAID, &rootID, &backingID, "a")
	linkB := testHeader(objectKindHardLink, linkBID, &childDirID, &backingID, "b")

	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{
		encodeInitial(t, root), encodeInitial(t, childDir), encodeInitial(t, nestedDir),
		encodeInitial(t, backing), encodeInitial(t, linkA), encodeInitial(t, linkB),
	}); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[shared.ObjectID]uint32{
		rootID: 3, childDirID: 3, nestedDirID: 2, linkAID: 2, linkBID: 2,
	} {
		got, err := layer.GetAttr(GetAttrRequest{ObjectID: id})
		if err != nil {
			t.Fatalf("GetAttr(%s): %v", id, err)
		}
		if got.Attributes.Nlink != want {
			t.Errorf("GetAttr(%s).Nlink = %d, want %d", id, got.Attributes.Nlink, want)
		}
	}
}
