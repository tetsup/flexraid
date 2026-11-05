package orchestration

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
)

func testID(value string) shared.ObjectID {
	id, err := shared.ParseObjectID(value)
	if err != nil {
		panic(err)
	}
	return id
}

func testHeader(kind shared.ObjectKind, id shared.ObjectID, parent, target *shared.ObjectID, name string) header {
	h := sampleHeader()
	h.Kind, h.ObjectID, h.ParentID, h.TargetID, h.Name = kind, id, parent, target, name
	h.Deleted = false
	if kind == objectKindDirectory || kind == objectKindHardLinkedFile {
		h.ParentID = nil
	}
	if kind != objectKindHardLink {
		h.TargetID = nil
	}
	return h
}

func encodeInitial(t *testing.T, h header) initialObject {
	t.Helper()
	data, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return initialObject{objectID: h.ObjectID, data: data}
}

func TestDecodeInitialHeader(t *testing.T) {
	want := sampleHeader()
	data, err := want.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("stripe tail")...)
	got, err := decodeInitialHeader(want.ObjectID, data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDecodeInitialHeaderRejectsInvalidInput(t *testing.T) {
	want := sampleHeader()
	data, err := want.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		objectID shared.ObjectID
		data     []byte
		wantErr  error
	}{
		{name: "short data", objectID: want.ObjectID, data: data[:headerSize-1], wantErr: errInvalidHeader},
		{name: "object ID mismatch", objectID: testID("22345678-9abc-4def-8abc-1234567890ab"), data: data, wantErr: errInvalidHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeInitialHeader(tt.objectID, tt.data)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestInitializeBuildsNamespaceAndResolvesHardLinks(t *testing.T) {
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	fileID := testID("22345678-9abc-4def-8abc-1234567890ab")
	backingID := testID("32345678-9abc-4def-8abc-1234567890ab")
	linkID := testID("42345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	file := testHeader(objectKindFile, fileID, &rootID, nil, "ordinary")
	backing := testHeader(objectKindHardLinkedFile, backingID, nil, nil, "")
	link := testHeader(objectKindHardLink, linkID, &rootID, &backingID, "linked")

	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{
		encodeInitial(t, link), encodeInitial(t, backing), encodeInitial(t, file), encodeInitial(t, root),
	}); err != nil {
		t.Fatal(err)
	}
	if layer.root == nil || layer.root.header.ObjectID != rootID {
		t.Fatal("root was not restored")
	}
	if layer.root.children["ordinary"] != layer.objects[fileID] || layer.root.children["linked"] != layer.objects[linkID] {
		t.Fatal("directory children were not reconstructed")
	}
	if layer.objects[fileID].file == nil || layer.objects[linkID].file == nil {
		t.Fatal("runtime file backing is missing")
	}
	if layer.objects[linkID].file != layer.objects[backingID].file {
		t.Fatal("HardLink does not share its HardLinkedFile runtime backing")
	}
	if len(layer.protectedObjects) != 0 || len(layer.warnings) != 0 {
		t.Fatalf("valid namespace unexpectedly protected objects or warned: %v %v", layer.protectedObjects, layer.warnings)
	}
}

func TestInitializeProtectsBrokenHardLinkAndOrphanWithoutFailing(t *testing.T) {
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	linkID := testID("42345678-9abc-4def-8abc-1234567890ab")
	missingTarget := testID("52345678-9abc-4def-8abc-1234567890ab")
	orphanID := testID("62345678-9abc-4def-8abc-1234567890ab")
	orphanParent := testID("72345678-9abc-4def-8abc-1234567890ab")
	root := testHeader(objectKindDirectory, rootID, nil, nil, "")
	link := testHeader(objectKindHardLink, linkID, &rootID, &missingTarget, "broken")
	orphan := testHeader(objectKindFile, orphanID, &orphanParent, nil, "orphan")

	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{encodeInitial(t, root), encodeInitial(t, link), encodeInitial(t, orphan)}); err != nil {
		t.Fatal(err)
	}
	if _, exists := layer.root.children["broken"]; exists {
		t.Fatal("broken HardLink was exposed in Namespace")
	}
	for _, id := range []shared.ObjectID{linkID, orphanID} {
		if _, protected := layer.protectedObjects[id]; !protected {
			t.Errorf("object %s is not protected", id)
		}
	}
	if len(layer.warnings) < 2 {
		t.Fatalf("got %d warnings, want at least 2", len(layer.warnings))
	}
}

func TestInitializeRejectsMissingOrMultipleRoots(t *testing.T) {
	rootID := testID("12345678-9abc-4def-8abc-1234567890ab")
	otherRootID := testID("22345678-9abc-4def-8abc-1234567890ab")
	tests := []struct {
		name    string
		objects []initialObject
	}{
		{name: "missing root"},
		{name: "multiple roots", objects: []initialObject{
			encodeInitial(t, testHeader(objectKindDirectory, rootID, nil, nil, "")),
			encodeInitial(t, testHeader(objectKindDirectory, otherRootID, nil, nil, "")),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layer := &Orchestration{}
			if err := layer.initialize(tt.objects); !errors.Is(err, errInvalidNamespace) {
				t.Fatalf("got %v, want errInvalidNamespace", err)
			}
			if len(layer.objects) != 0 {
				t.Fatal("failed initialization replaced runtime state")
			}
		})
	}
}

func TestInitializeDoesNotReplaceStateOnFailure(t *testing.T) {
	existing := sampleHeader()
	layer := &Orchestration{headers: map[shared.ObjectID]header{existing.ObjectID: existing}}
	invalid := initialObject{objectID: existing.ObjectID, data: []byte{1, 2, 3}}
	if err := layer.initialize([]initialObject{invalid}); !errors.Is(err, errInvalidHeader) {
		t.Fatalf("got %v, want errInvalidHeader", err)
	}
	if len(layer.headers) != 1 || !reflect.DeepEqual(layer.headers[existing.ObjectID], existing) {
		t.Fatal("existing headers changed after initialization failure")
	}
}

func TestInitializeRejectsDuplicateObjectIDs(t *testing.T) {
	objectHeader := sampleHeader()
	data, err := objectHeader.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	initial := initialObject{objectID: objectHeader.ObjectID, data: data}
	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{initial, initial}); !errors.Is(err, errInvalidHeader) {
		t.Fatalf("got %v, want errInvalidHeader", err)
	}
	if len(layer.headers) != 0 {
		t.Fatal("state should remain uninitialized after duplicate IDs")
	}
}
