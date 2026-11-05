package orchestration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func TestReadReturnsLogicalRangeAcrossStripesAndHardLinks(t *testing.T) {
	ctx := context.Background()
	ios := []*physicalio.IO{
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}

	fileID := testID("32345678-9abc-4def-8abc-1234567890ab")
	h := testHeader(objectKindFile, fileID, ptrObjectID(testID("12345678-9abc-4def-8abc-1234567890ab")), nil, "data")
	h.Layout = layout{Mode: layoutModeMirror, ChunkSize: 64, DataChunks: 2}
	payload := bytes.Repeat([]byte("abcdefgh"), 23)
	h.Size = uint64(len(payload))
	headerBytes, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	logical := append(headerBytes, payload...)
	for offset, stripeID := 0, storage.StripeID(0); offset < len(logical); stripeID++ {
		end := offset + int(h.Layout.ChunkSize)
		if end > len(logical) {
			end = len(logical)
		}
		_, err := distributed.Layer.WriteStripe(distribution.WriteStripeRequest{
			Context: ctx, ObjectID: fileID, StripeID: stripeID, ChunkSize: h.Layout.ChunkSize,
			Layout:    distribution.Layout{Type: distribution.LayoutMirror, NumChunks: uint8(h.Layout.DataChunks)},
			IOIndexes: []int{0, 1}, Data: logical[offset:end],
		})
		if err != nil {
			t.Fatal(err)
		}
		offset = end
	}

	layer := &Orchestration{
		distribution: distributed.Layer,
		objects: map[shared.ObjectID]*runtimeObject{
			fileID: {header: h, file: &runtimeFile{objectID: fileID, header: h}},
		},
	}
	linkID := testID("42345678-9abc-4def-8abc-1234567890ab")
	layer.objects[linkID] = &runtimeObject{
		header: testHeader(objectKindHardLink, linkID, h.ParentID, &fileID, "alias"),
		file:   layer.objects[fileID].file,
	}

	openResponse, err := layer.Open(OpenRequest{ObjectID: fileID})
	if err != nil {
		t.Fatal(err)
	}
	defer layer.Release(ReleaseRequest{Handle: openResponse.Handle})

	for _, tc := range []struct {
		offset uint64
		size   uint32
	}{
		{offset: 0, size: 20},
		{offset: 17, size: 130},
		{offset: uint64(len(payload) - 11), size: 40},
		{offset: uint64(len(payload)), size: 10},
	} {
		got, err := layer.Read(ReadRequest{Context: ctx, Handle: openResponse.Handle, ObjectID: fileID, Offset: tc.offset, Size: tc.size})
		if err != nil {
			t.Fatalf("Read offset=%d size=%d: %v", tc.offset, tc.size, err)
		}
		end := tc.offset + uint64(tc.size)
		if end > uint64(len(payload)) {
			end = uint64(len(payload))
		}
		if !bytes.Equal(got.Data, payload[tc.offset:end]) {
			t.Fatalf("Read offset=%d size=%d returned %d bytes, want %d", tc.offset, tc.size, len(got.Data), end-tc.offset)
		}
	}
	linkOpen, err := layer.Open(OpenRequest{ObjectID: linkID})
	if err != nil {
		t.Fatal(err)
	}
	defer layer.Release(ReleaseRequest{Handle: linkOpen.Handle})
	got, err := layer.Read(ReadRequest{Context: ctx, Handle: linkOpen.Handle, ObjectID: linkID, Offset: 9, Size: 100})
	if err != nil || !bytes.Equal(got.Data, payload[9:109]) {
		t.Fatalf("HardLink Read = %d bytes, %v", len(got.Data), err)
	}
}

func TestReadValidatesObjectAndContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	layer := &Orchestration{objects: make(map[shared.ObjectID]*runtimeObject)}
	id := testID("32345678-9abc-4def-8abc-1234567890ab")
	if _, err := layer.Read(ReadRequest{Context: ctx, ObjectID: id, Size: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if _, err := layer.Read(ReadRequest{ObjectID: id, Size: 1}); err == nil {
		t.Fatal("missing object unexpectedly read")
	}
	layer.objects[id] = &runtimeObject{header: testHeader(objectKindDirectory, id, nil, nil, "")}
	if _, err := layer.Read(ReadRequest{ObjectID: id, Size: 1}); err == nil {
		t.Fatal("directory unexpectedly readable")
	}
}

func TestReadRequiresValidHandle(t *testing.T) {
	id := testID("32345678-9abc-4def-8abc-1234567890ab")
	file := &runtimeFile{objectID: id, header: sampleHeader()}
	objectHeader := testHeader(objectKindFile, id, ptrObjectID(testID("12345678-9abc-4def-8abc-1234567890ab")), nil, "data")
	objectHeader.Layout = layout{Mode: layoutModeMirror, ChunkSize: 64, DataChunks: 2}
	file.header = objectHeader
	layer := &Orchestration{objects: map[shared.ObjectID]*runtimeObject{id: {header: objectHeader, file: file}}}

	if _, err := layer.Read(ReadRequest{ObjectID: id, Size: 1}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Read without handle = %v, want os.ErrInvalid", err)
	}
	opened, err := layer.Open(OpenRequest{ObjectID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layer.Release(ReleaseRequest{Handle: opened.Handle}); err != nil {
		t.Fatal(err)
	}
	if _, err := layer.Read(ReadRequest{Handle: opened.Handle, ObjectID: id, Size: 1}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Read after Release = %v, want os.ErrInvalid", err)
	}
}
