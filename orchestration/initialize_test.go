package orchestration

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
)

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
		{name: "object ID mismatch", objectID: shared.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xac}, data: data, wantErr: errInvalidHeader},
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

func TestInitializeBuildsHeaderAndDeletionMaps(t *testing.T) {
	active := sampleHeader()
	deleted := sampleHeader()
	deleted.ObjectID = shared.ObjectID{0x22, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab}
	deleted.Deleted = true

	activeData, err := active.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	deletedData, err := deleted.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	layer := &Orchestration{}
	if err := layer.initialize([]initialObject{
		{objectID: active.ObjectID, data: activeData},
		{objectID: deleted.ObjectID, data: deletedData},
	}); err != nil {
		t.Fatal(err)
	}

	if len(layer.headers) != 2 {
		t.Fatalf("got %d headers, want 2", len(layer.headers))
	}
	if _, exists := layer.headers[active.ObjectID]; !exists {
		t.Fatal("active header missing")
	}
	if _, exists := layer.headers[deleted.ObjectID]; !exists {
		t.Fatal("deleted header missing")
	}
	if len(layer.deletionQueue) != 1 {
		t.Fatalf("got %d deletion queue entries, want 1", len(layer.deletionQueue))
	}
	if _, exists := layer.deletionQueue[deleted.ObjectID]; !exists {
		t.Fatal("deleted object missing from deletion queue")
	}
}

func TestInitializeDoesNotReplaceStateOnFailure(t *testing.T) {
	existing := sampleHeader()
	layer := &Orchestration{
		headers:       map[shared.ObjectID]header{existing.ObjectID: existing},
		deletionQueue: map[shared.ObjectID]struct{}{existing.ObjectID: {}},
	}
	invalid := initialObject{objectID: existing.ObjectID, data: []byte{1, 2, 3}}

	if err := layer.initialize([]initialObject{invalid}); !errors.Is(err, errInvalidHeader) {
		t.Fatalf("got %v, want errInvalidHeader", err)
	}
	if len(layer.headers) != 1 || !reflect.DeepEqual(layer.headers[existing.ObjectID], existing) {
		t.Fatal("existing headers changed after initialization failure")
	}
	if len(layer.deletionQueue) != 1 {
		t.Fatal("existing deletion queue changed after initialization failure")
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
	if len(layer.headers) != 0 || len(layer.deletionQueue) != 0 {
		t.Fatal("state should remain uninitialized after duplicate IDs")
	}
}
