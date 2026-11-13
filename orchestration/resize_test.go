package orchestration

import (
	"bytes"
	"context"
	"syscall"
	"testing"

	"github.com/tetsup/flexraid/distribution"
	cfg "github.com/tetsup/flexraid/pkg/shared/config"
	"github.com/tetsup/flexraid/storage"
)

func newMultiStripeWriteFixture(t *testing.T, chunkSize uint32, regularStripes int, tail uint64) (writeFixture, []byte) {
	t.Helper()
	fixture := newWriteFixture(t)
	firstCapacity := uint64(cfg.MinimumChunkSize)*3 - cfg.HeaderReservedSize
	regularCapacity := uint64(chunkSize) * 3
	size := firstCapacity + uint64(regularStripes-1)*regularCapacity + tail
	payload := make([]byte, int(size))
	for i := range payload {
		payload[i] = byte((i*31 + 17) % 251)
	}

	h := fixture.layer.objects[fixture.fileID].file.header
	h.Size = size
	h.Layout = layout{Mode: layoutModeStripe, ChunkSize: chunkSize, DataChunks: 3, ParityChunks: 1}
	headerBytes, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, int(cfg.HeaderReservedSize)+int(firstCapacity))
	copy(first, headerBytes)
	copy(first[int(cfg.HeaderReservedSize):], payload[:firstCapacity])
	if _, err := fixture.layer.distribution.WriteStripe(distribution.WriteStripeRequest{
		Context: context.Background(), ObjectID: fixture.fileID, StripeID: 0,
		ChunkSize: cfg.MinimumChunkSize, Layout: distribution.Layout{Type: distribution.LayoutParity, NumChunks: 3},
		IOIndexes: []int{0, 1, 2, 3}, Data: first,
	}); err != nil {
		t.Fatal(err)
	}
	offset := firstCapacity
	for stripeID := 1; offset < size; stripeID++ {
		end := offset + regularCapacity
		if end > size {
			end = size
		}
		if _, err := fixture.layer.distribution.WriteStripe(distribution.WriteStripeRequest{
			Context: context.Background(), ObjectID: fixture.fileID, StripeID: storage.StripeID(stripeID),
			ChunkSize: chunkSize, Layout: distribution.Layout{Type: distribution.LayoutParity, NumChunks: 3},
			IOIndexes: []int{0, 1, 2, 3}, Data: payload[int(offset):int(end)],
		}); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	file := fixture.layer.objects[fixture.fileID].file
	file.header = h
	file.stripeCache = make(map[uint64]*stripeCacheEntry)
	fixture.layer.objects[fixture.fileID].header = h
	return fixture, payload
}

func TestWriteGrowthPreservesExistingMultiStripeLayoutAndData(t *testing.T) {
	fixture, payload := newMultiStripeWriteFixture(t, 16*1024, 3, 777)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	offset := uint64(len(payload) + 19)
	patch := []byte("new-data")
	if _, err := fixture.layer.Write(WriteRequest{
		Handle: writer, ObjectID: fixture.fileID, Offset: offset, Data: patch,
	}); err != nil {
		t.Fatal(err)
	}

	file := fixture.layer.objects[fixture.fileID].file
	if file.header.Layout.ChunkSize != 16*1024 ||
		file.header.Layout.Mode != layoutModeStripe ||
		file.header.Layout.DataChunks != 3 ||
		file.header.Layout.ParityChunks != 1 {
		t.Fatalf("multi-stripe layout changed during growth: %+v", file.header.Layout)
	}
	for _, unchanged := range []uint64{1, 2} {
		if _, cached := file.stripeCache[unchanged]; cached {
			t.Fatalf("unchanged full stripe %d was needlessly rebuilt", unchanged)
		}
	}
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	got, err := fixture.layer.Read(ReadRequest{
		Handle: reader, ObjectID: fixture.fileID, Offset: 0, Size: uint32(len(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, payload) {
		t.Fatal("growth changed existing file data")
	}
	gap, err := fixture.layer.Read(ReadRequest{
		Handle: reader, ObjectID: fixture.fileID, Offset: uint64(len(payload)), Size: uint32(len(patch) + 19),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantGap := append(make([]byte, 19), patch...)
	if !bytes.Equal(gap.Data, wantGap) {
		t.Fatalf("extension gap/data = %v, want %v", gap.Data, wantGap)
	}
}

func TestSparseWriteSharesSingleZeroStripeBuffer(t *testing.T) {
	fixture, payload := newMultiStripeWriteFixture(t, 16*1024, 3, 777)
	writer := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	firstCapacity := uint64(cfg.MinimumChunkSize)*3 - cfg.HeaderReservedSize
	regularCapacity := uint64(16*1024) * 3
	offset := uint64(len(payload)) + 4*regularCapacity + 11
	if _, err := fixture.layer.Write(WriteRequest{
		Handle: writer, ObjectID: fixture.fileID, Offset: offset, Data: []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}

	// Fully zero-filled intermediate stripes are represented by one compact
	// range descriptor; no per-stripe byte buffers or cache entries are retained.
	file := fixture.layer.objects[fixture.fileID].file
	if len(file.zeroStripeRanges) != 1 ||
		file.zeroStripeRanges[0] != (stripeRange{start: 4, end: 6}) {
		t.Fatalf("zero stripe ranges = %+v, want [4,6]", file.zeroStripeRanges)
	}
	for _, id := range []uint64{4, 5, 6} {
		if _, cached := file.stripeCache[id]; cached {
			t.Fatalf("zero stripe %d unexpectedly has a materialized cache entry", id)
		}
	}
	if len(file.stripeCache) > 5 {
		t.Fatalf("sparse write materialized too many stripes: %d", len(file.stripeCache))
	}
	if _, err := fixture.layer.Flush(FlushRequest{Handle: writer}); err != nil {
		t.Fatal(err)
	}
	fixture.layer.objects[fixture.fileID].file.stripeCache = make(map[uint64]*stripeCacheEntry)
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	gapStart := uint64(len(payload))
	gapSize := offset - gapStart
	for _, sample := range []uint64{0, firstCapacity, firstCapacity + regularCapacity, gapSize - 1} {
		got, err := fixture.layer.Read(ReadRequest{
			Handle: reader, ObjectID: fixture.fileID, Offset: gapStart + sample, Size: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Data, []byte{0}) {
			t.Fatalf("gap byte at %d = %v, want zero", sample, got.Data)
		}
	}
}

func TestTruncateToOneStripeDropsTailCacheAndPreservesPrefix(t *testing.T) {
	fixture, payload := newMultiStripeWriteFixture(t, 16*1024, 3, 777)
	handle := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
	reader := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDONLY))
	if _, err := fixture.layer.Read(ReadRequest{
		Handle: reader, ObjectID: fixture.fileID, Offset: 0, Size: uint32(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	size := uint64(1024)
	if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: fixture.fileID, Handle: handle, Size: &size}); err != nil {
		t.Fatal(err)
	}
	file := fixture.layer.objects[fixture.fileID].file
	if len(file.stripeCache) != 1 || file.stripeCache[0] == nil {
		t.Fatalf("truncate retained out-of-range stripe cache entries: %d", len(file.stripeCache))
	}
	if file.header.Size != size {
		t.Fatalf("truncated size = %d, want %d", file.header.Size, size)
	}
	if _, err := fixture.layer.Flush(FlushRequest{Handle: handle}); err != nil {
		t.Fatal(err)
	}
	file.stripeCache = make(map[uint64]*stripeCacheEntry)
	got, err := fixture.layer.Read(ReadRequest{Handle: reader, ObjectID: fixture.fileID, Offset: 0, Size: uint32(size)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, payload[:size]) {
		t.Fatal("truncate changed retained prefix")
	}
}

func TestMaximumFileSizeIsFixedAt16TiB(t *testing.T) {
	fixture := newWriteFixture(t)
	old := fixture.layer.objects[fixture.fileID].file.header

	if err := fixture.layer.validateResizeCapacity(context.Background(), old, cfg.MaxFileSize+1); err != syscall.EFBIG {
		t.Fatalf("size above MaxFileSize = %v, want EFBIG", err)
	}
	if err := fixture.layer.validateResizeCapacity(context.Background(), old, cfg.MaxFileSize); err == syscall.EFBIG {
		t.Fatalf("size equal to MaxFileSize returned EFBIG")
	}
}

func TestHugeResizeIsRejectedBeforePlacementExpansion(t *testing.T) {
	const hugeSize = uint64(1) << 50

	t.Run("SetAttr", func(t *testing.T) {
		fixture := newWriteFixture(t)
		before := fixture.layer.objects[fixture.fileID].file.header
		if _, err := fixture.layer.SetAttr(SetAttrRequest{ObjectID: fixture.fileID, Size: ptrUint64(hugeSize)}); err != syscall.EFBIG {
			t.Fatalf("SetAttr huge size = %v, want EFBIG", err)
		}
		if got := fixture.layer.objects[fixture.fileID].file.header; got != before {
			t.Fatal("failed SetAttr changed the live Header")
		}
	})

	t.Run("Truncate", func(t *testing.T) {
		fixture := newWriteFixture(t)
		handle := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
		before := fixture.layer.objects[fixture.fileID].file.header
		if _, err := fixture.layer.Truncate(TruncateRequest{Handle: handle, ObjectID: fixture.fileID, Size: hugeSize}); err != syscall.EFBIG {
			t.Fatalf("Truncate huge size = %v, want EFBIG", err)
		}
		if got := fixture.layer.objects[fixture.fileID].file.header; got != before {
			t.Fatal("failed Truncate changed the live Header")
		}
	})

	t.Run("Write", func(t *testing.T) {
		fixture := newWriteFixture(t)
		handle := openWriteHandle(t, fixture.layer, fixture.fileID, uint32(syscall.O_RDWR))
		before := fixture.layer.objects[fixture.fileID].file.header
		if _, err := fixture.layer.Write(WriteRequest{Handle: handle, ObjectID: fixture.fileID, Offset: hugeSize, Data: []byte("x")}); err != syscall.EFBIG {
			t.Fatalf("Write huge offset = %v, want EFBIG", err)
		}
		if got := fixture.layer.objects[fixture.fileID].file.header; got != before {
			t.Fatal("failed Write changed the live Header")
		}
	})
}

func ptrUint64(value uint64) *uint64 { return &value }
