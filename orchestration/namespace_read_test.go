package orchestration

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
)

func initializedReadLayer(t *testing.T) (*Orchestration, shared.ObjectID, shared.ObjectID, shared.ObjectID) {
	t.Helper()
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	dirID := testID("22345678-9abc-4def-8abc-1234567890ab")
	fileID := testID("32345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	dir := testHeader(objectKindDirectory, dirID, &rootID, nil, "docs")
	file := testHeader(objectKindFile, fileID, &dirID, nil, "readme")
	file.Mode, file.UID, file.GID, file.Nlink, file.Size = 0644, 100, 200, 1, 123
	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{encodeInitial(t, file), encodeInitial(t, root), encodeInitial(t, dir)}); err != nil { t.Fatal(err) }
	return layer, rootID, dirID, fileID
}

func TestLookup(t *testing.T) {
	layer, rootID, dirID, fileID := initializedReadLayer(t)
	got, err := layer.Lookup(LookupRequest{ParentID: rootID, Name: "docs"})
	if err != nil { t.Fatal(err) }
	if got.ObjectID != dirID || got.Kind != objectKindDirectory { t.Fatalf("unexpected lookup result: %#v", got) }
	got, err = layer.Lookup(LookupRequest{ParentID: dirID, Name: "readme"})
	if err != nil || got.ObjectID != fileID { t.Fatalf("lookup file: result=%#v err=%v", got, err) }
	if _, err := layer.Lookup(LookupRequest{ParentID: rootID, Name: "missing"}); !errors.Is(err, os.ErrNotExist) { t.Fatalf("got %v, want os.ErrNotExist", err) }
	if _, err := layer.Lookup(LookupRequest{ParentID: fileID, Name: "child"}); !errors.Is(err, errNotDirectory) { t.Fatalf("got %v, want errNotDirectory", err) }
	if _, err := layer.Lookup(LookupRequest{ParentID: rootID, Name: "../docs"}); err == nil { t.Fatal("invalid child name accepted") }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if _, err := layer.Lookup(LookupRequest{Context: ctx, ParentID: rootID, Name: "docs"}); !errors.Is(err, context.Canceled) { t.Fatalf("got %v, want context.Canceled", err) }
}

func TestReadDir(t *testing.T) {
	layer, rootID, dirID, fileID := initializedReadLayer(t)
	got, err := layer.ReadDir(ReadDirRequest{ObjectID: rootID})
	if err != nil { t.Fatal(err) }
	want := []DirectoryEntry{{ObjectID: dirID, Kind: objectKindDirectory, Name: "docs"}}
	if !reflect.DeepEqual(got.Entries, want) { t.Fatalf("got %#v, want %#v", got.Entries, want) }
	got, err = layer.ReadDir(ReadDirRequest{ObjectID: dirID})
	if err != nil { t.Fatal(err) }
	want = []DirectoryEntry{{ObjectID: fileID, Kind: objectKindFile, Name: "readme"}}
	if !reflect.DeepEqual(got.Entries, want) { t.Fatalf("got %#v, want %#v", got.Entries, want) }
	if _, err := layer.ReadDir(ReadDirRequest{ObjectID: fileID}); !errors.Is(err, errNotDirectory) { t.Fatalf("got %v, want errNotDirectory", err) }
}

func TestGetAttr(t *testing.T) {
	layer, _, _, fileID := initializedReadLayer(t)
	got, err := layer.GetAttr(GetAttrRequest{ObjectID: fileID})
	if err != nil { t.Fatal(err) }
	attr := got.Attributes
	if attr.ObjectID != fileID || attr.Kind != objectKindFile || attr.Name != "readme" || attr.Mode != 0644 || attr.UID != 100 || attr.GID != 200 || attr.Nlink != 1 || attr.Size != 123 { t.Fatalf("unexpected attributes: %#v", attr) }
	missing := testID("42345678-9abc-4def-8abc-1234567890ab")
	if _, err := layer.GetAttr(GetAttrRequest{ObjectID: missing}); !errors.Is(err, os.ErrNotExist) { t.Fatalf("got %v, want os.ErrNotExist", err) }
}

func TestResolvePath(t *testing.T) {
	layer, rootID, dirID, fileID := initializedReadLayer(t)
	for input, expected := range map[string]shared.ObjectID{"/": rootID, "/docs": dirID, "/docs/readme": fileID, "/docs/./readme": fileID, "/docs/../docs/readme": fileID} {
		got, err := layer.resolveObjectID(input)
		if err != nil || got != expected { t.Errorf("resolveObjectID(%q) = %s, %v; want %s", input, got.String(), err, expected.String()) }
	}
	if _, err := layer.resolvePath("docs/readme"); err == nil { t.Fatal("relative path accepted") }
	if _, err := layer.resolvePath("/missing"); !errors.Is(err, errPathNotFound) { t.Fatalf("got %v, want errPathNotFound", err) }
	if _, err := layer.resolvePath("/docs/readme/child"); !errors.Is(err, errNotDirectory) { t.Fatalf("got %v, want errNotDirectory", err) }
}
