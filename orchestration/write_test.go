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
	"github.com/tetsup/flexraid/storage"
)

type writeFixture struct {
	layer   *Orchestration
	fileID  shared.ObjectID
	linkID  shared.ObjectID
	payload []byte
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
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
	for offset, stripeID := 0, storage.StripeID(0); offset < len(logical); stripeID++ {
		end := offset + int(h.Layout.ChunkSize)
		if end > len(logical) {
			end = len(logical)
		}
		_, err = distributed.Layer.WriteStripe(distribution.WriteStripeRequest{
			Context: ctx, ObjectID: fileID, StripeID: stripeID, ChunkSize: h.Layout.ChunkSize,
			Layout: distribution.Layout{Type: distribution.LayoutMirror, NumChunks: 2},
			IOIndexes: []int{0, 1}, Data: logical[offset:end],
		})
		if err != nil {
			t.Fatal(err)
		}
		offset = end
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
	return writeFixture{layer: layer, fileID: fileID, linkID: linkID, payload: payload}
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
	for stripeID, entry := range file.stripeCache {
		if entry.state != stripeCacheDirty {
			t.Errorf("stripe %d state = %d, want dirty", stripeID, entry.state)
		}
	}
	persisted, err := fixture.layer.distribution.ReadStripe(distribution.ReadStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID,
		StripeID: storage.StripeID((uint64(headerSize) + 53) / 64), ChunkSize: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted.Data, patch) {
		t.Fatal("Write persisted data before Flush/Fsync")
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
		data []byte
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
