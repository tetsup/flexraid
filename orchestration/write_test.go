package orchestration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	physicalio "github.com/tetsup/flexraid/io"
	"github.com/tetsup/flexraid/pkg/shared"
)

type writeFixture struct {
	layer      *Orchestration
	fileID     shared.ObjectID
	linkID     shared.ObjectID
	payload    []byte
	headerData []byte
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	ctx := context.Background()
	ios := []*physicalio.IO{
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
		physicalio.New(physicalio.NewRequest{Root: t.TempDir()}).Layer,
	}
	distributed, err := distribution.New(distribution.NewRequest{IOs: ios})
	if err != nil {
		t.Fatal(err)
	}
	fileID := testID("32345678-9abc-4def-8abc-1234567890ab")
	parentID := testID("12345678-9abc-4def-8abc-1234567890ab")
	h := testHeader(objectKindFile, fileID, &parentID, nil, "data")
	h.Layout = layout{Mode: layoutModeMirror, ChunkSize: 64, DataChunks: 2}
	payload := bytes.Repeat([]byte("abcdefgh"), 31)
	h.Size = uint64(len(payload))
	headerData, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	logical := append(headerData, payload...)
	_, err = distributed.Layer.WriteStripe(distribution.WriteStripeRequest{
		Context: ctx, ObjectID: fileID, StripeID: 0, ChunkSize: 4096,
		Layout:    distribution.Layout{Type: distribution.LayoutMirror, NumChunks: 2},
		IOIndexes: []int{0, 1}, Data: logical,
	})
	if err != nil {
		t.Fatal(err)
	}
	file := &runtimeFile{objectID: fileID, header: h, stripeCache: make(map[uint64]*stripeCacheEntry)}
	linkID := testID("42345678-9abc-4def-8abc-1234567890ab")
	layer := &Orchestration{
		distribution: distributed.Layer,
		objects: map[shared.ObjectID]*runtimeObject{
			fileID: {header: h, file: file},
			linkID: {header: testHeader(objectKindHardLink, linkID, &parentID, &fileID, "alias"), file: file},
		},
	}
	return writeFixture{layer: layer, fileID: fileID, linkID: linkID, payload: payload, headerData: append([]byte(nil), headerData...)}
}

func openWriteHandle(t *testing.T, layer *Orchestration, id shared.ObjectID, flags uint32) *FileHandle {
	t.Helper()
	opened, err := layer.Open(OpenRequest{ObjectID: id, Flags: flags})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = layer.Release(ReleaseRequest{Handle: opened.Handle}) })
	return opened.Handle
}

func TestWriteUpdatesSharedCacheVisibleToOtherHandle(t *testing.T) {
	fixture := newWriteFixture(t)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	reader := openWriteHandle(t, fixture.layer, fixture.linkID, uint32(syscall.O_RDONLY))
	patch := []byte("modified-across-stripes")
	if got, err := fixture.layer.Write(WriteRequest{Handle: writer, ObjectID: fixture.fileID, Offset: 53, Data: patch}); err != nil || got.Written != uint32(len(patch)) {
		t.Fatalf("Write = %+v, %v", got, err)
	}
	read, err := fixture.layer.Read(ReadRequest{Handle: reader, ObjectID: fixture.linkID, Offset: 45, Size: 45})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), fixture.payload[45:90]...)
	copy(want[8:], patch)
	if !bytes.Equal(read.Data, want) {
		t.Fatalf("Read after Write = %q, want %q", read.Data, want)
	}
	file := fixture.layer.objects[fixture.fileID].file
	file.mu.RLock()
	defer file.mu.RUnlock()
	if file.header.Size != uint64(len(fixture.payload)) {
		t.Fatalf("logical size = %d, want %d", file.header.Size, len(fixture.payload))
	}
	if entry := file.stripeCache[0]; entry == nil || entry.state != stripeCacheDirty {
		t.Fatalf("stripe 0 cache = %#v, want dirty", entry)
	}
	if len(file.stripeCache) != 1 {
		t.Fatalf("cached stripes = %d, want only stripe 0", len(file.stripeCache))
	}
	persisted, err := fixture.layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID,
		StripeID: 0, ChunkSize: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := append(append([]byte(nil), fixture.headerData...), fixture.payload...)
	if !bytes.Equal(persisted.Data, original) {
		t.Fatal("Write changed persisted data before Flush/Fsync")
	}
}

func TestWriteExtendsFileAndZeroFillsGap(t *testing.T) {
	fixture := newWriteFixture(t)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	offset := uint64(len(fixture.payload) + 7)
	patch := []byte("tail")
	if _, err := fixture.layer.Write(WriteRequest{Handle: writer, ObjectID: fixture.fileID, Offset: offset, Data: patch}); err != nil {
		t.Fatal(err)
	}
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	read, err := fixture.layer.Read(ReadRequest{Handle: reader, ObjectID: fixture.fileID, Offset: uint64(len(fixture.payload)), Size: 11})
	if err != nil {
		t.Fatal(err)
	}
	want := append(make([]byte, 7), patch...)
	if !bytes.Equal(read.Data, want) {
		t.Fatalf("extended range = %v, want %v", read.Data, want)
	}
	attrs, err := fixture.layer.GetAttr(GetAttrRequest{ObjectID: fixture.fileID})
	if err != nil || attrs.Attributes.Size != offset+uint64(len(patch)) {
		t.Fatalf("GetAttr = %+v, %v", attrs, err)
	}
}

func TestWriteAppendUsesSharedCurrentSize(t *testing.T) {
	fixture := newWriteFixture(t)
	first := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_WRONLY|syscall.O_APPEND))
	second := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_WRONLY|syscall.O_APPEND))
	for _, item := range []struct {
		handle *FileHandle
		data   []byte
	}{{first, []byte("one")}, {second, []byte("two")}} {
		if _, err := fixture.layer.Write(WriteRequest{Handle: item.handle, ObjectID: fixture.fileID, Data: item.data}); err != nil {
			t.Fatal(err)
		}
	}
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	read, err := fixture.layer.Read(ReadRequest{Handle: reader, ObjectID: fixture.fileID, Offset: uint64(len(fixture.payload)), Size: 6})
	if err != nil || !bytes.Equal(read.Data, []byte("onetwo")) {
		t.Fatalf("append Read = %q, %v", read.Data, err)
	}
}

func TestWriteRejectsInvalidHandleAndAccessMode(t *testing.T) {
	fixture := newWriteFixture(t)
	if _, err := fixture.layer.Write(WriteRequest{ObjectID: fixture.fileID, Data: []byte("x")}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Write without handle = %v", err)
	}
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	if _, err := fixture.layer.Write(WriteRequest{Handle: reader, ObjectID: fixture.fileID, Data: []byte("x")}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Write through read-only handle = %v", err)
	}
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_WRONLY))
	if _, err := fixture.layer.Write(WriteRequest{Handle: writer, ObjectID: fixture.linkID, Data: []byte("x")}); err != nil {
		t.Fatalf("Write through HardLink = %v", err)
	}
}

func TestWriteRejectsSyncModeUntilPersistenceExists(t *testing.T) {
	fixture := newWriteFixture(t)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_WRONLY|syscall.O_SYNC))
	if _, err := fixture.layer.Write(WriteRequest{Handle: writer, ObjectID: fixture.fileID, Data: []byte("x")}); !errors.Is(err, syscall.ENOTSUP) {
		t.Fatalf("synchronous Write = %v, want ENOTSUP", err)
	}
}

func TestWriteCancellationDoesNotChangeCache(t *testing.T) {
	fixture := newWriteFixture(t)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.layer.Write(WriteRequest{Context: ctx, Handle: writer, ObjectID: fixture.fileID, Data: []byte("x")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Write = %v", err)
	}
	file := fixture.layer.objects[fixture.fileID].file
	file.mu.RLock()
	defer file.mu.RUnlock()
	if len(file.stripeCache) != 0 || file.header.Size != uint64(len(fixture.payload)) {
		t.Fatal("canceled Write changed runtime file state")
	}
}

func TestWriteReplansAndFlushesAcrossMirrorParityBoundary(t *testing.T) {
	fixture := newWriteFixture(t)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	if _, err := fixture.layer.Write(WriteRequest{Handle: writer, ObjectID: fixture.fileID, Offset: 3072, Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	file := fixture.layer.objects[fixture.fileID].file
	file.mu.RLock()
	if file.header.Size != 3073 || file.header.Layout.Mode != layoutModeStripe {
		file.mu.RUnlock()
		t.Fatalf("updated header = %+v", file.header)
	}
	file.mu.RUnlock()
	flushed, err := fixture.layer.Flush(FlushRequest{Handle: writer})
	if err != nil || flushed.PersistedStripes != 1 {
		t.Fatalf("Flush = %+v, %v", flushed, err)
	}
	file.mu.Lock()
	file.stripeCache = make(map[uint64]*stripeCacheEntry)
	file.mu.Unlock()
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	got, err := fixture.layer.Read(ReadRequest{Handle: reader, ObjectID: fixture.fileID, Offset: 3072, Size: 1})
	if err != nil || !bytes.Equal(got.Data, []byte("x")) {
		t.Fatalf("Read after replan+Flush = %q, %v", got.Data, err)
	}
}

func TestTruncateReplansAndZeroFillsGrowth(t *testing.T) {
	fixture := newWriteFixture(t)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	if _, err := fixture.layer.Truncate(TruncateRequest{Handle: writer, ObjectID: fixture.fileID, Size: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.layer.Truncate(TruncateRequest{Handle: writer, ObjectID: fixture.fileID, Size: 120}); err != nil {
		t.Fatal(err)
	}
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	got, err := fixture.layer.Read(ReadRequest{Handle: reader, ObjectID: fixture.fileID, Offset: 100, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, make([]byte, 20)) {
		t.Fatalf("truncate growth = %v, want zero-filled bytes", got.Data)
	}
}
