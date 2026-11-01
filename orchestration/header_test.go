package orchestration

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

func sampleHeader() Header {
	return Header{
		FormatVersion: HeaderFormatVersion,
		ObjectID: shared.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab},
		ParentID: ptrObjectID(shared.ObjectID{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x4a, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0}),
		Kind: ObjectKindFile,
		Name: "メディア.bin",
		Mode: 0644,
		UID: 1000,
		GID: 100,
		Nlink: 1,
		Size: 987654321,
		Atime: time.Unix(1700000000, 123456789),
		Mtime: time.Unix(1700000001, 234567890),
		Ctime: time.Unix(1700000002, 345678901),
		Layout: Layout{Mode: LayoutModeStripe, ChunkSize: 1 << 20, DataChunks: 4, ParityChunks: 1},
	}
}

func TestHeaderBinaryRoundTrip(t *testing.T) {
	want := sampleHeader()
	want.TargetID = ptrObjectID(shared.ObjectID{0x21, 0x43, 0x65, 0x87, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab})
	want.Kind = ObjectKindHardLink
	data, err := want.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != HeaderSize {
		t.Fatalf("got %d want %d", len(data), HeaderSize)
	}
	var got Header
	if err := got.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if got.ObjectID != want.ObjectID || !equalOptionalObjectID(got.ParentID, want.ParentID) ||
		!equalOptionalObjectID(got.TargetID, want.TargetID) || got.FormatVersion != want.FormatVersion ||
		got.Kind != want.Kind || got.Name != want.Name || got.Mode != want.Mode || got.UID != want.UID ||
		got.GID != want.GID || got.Nlink != want.Nlink || got.Size != want.Size ||
		!got.Atime.Equal(want.Atime) || !got.Mtime.Equal(want.Mtime) || !got.Ctime.Equal(want.Ctime) || got.Layout != want.Layout {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestHeaderMarshalUsesVersionZero(t *testing.T) {
	h := sampleHeader()
	h.FormatVersion = 0
	b, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var got Header
	if err := got.UnmarshalBinary(b); err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != HeaderFormatVersion {
		t.Fatal(got.FormatVersion)
	}
}

func TestHeaderMarshalValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Header)
		want   error
	}{
		{"unsupported version", func(h *Header) { h.FormatVersion = 1 }, ErrUnsupportedFormat},
		{"long name", func(h *Header) { h.Name = string(bytes.Repeat([]byte("x"), MaxNameSize+1)) }, ErrNameTooLong},
		{"invalid utf8", func(h *Header) { h.Name = string([]byte{0xff}) }, ErrInvalidName},
		{"unknown kind", func(h *Header) { h.Kind = shared.ObjectKind(99) }, ErrInvalidHeader},
		{"file without parent", func(h *Header) { h.ParentID = nil }, ErrInvalidHeader},
		{"file with target", func(h *Header) { h.TargetID = ptrObjectID(shared.ObjectID{0x21, 0x43, 0x65, 0x87, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab}) }, ErrInvalidHeader},
		{"hard link without target", func(h *Header) { h.Kind = ObjectKindHardLink }, ErrInvalidHeader},
		{"hard link without parent", func(h *Header) { h.Kind = ObjectKindHardLink; h.TargetID = ptrObjectID(shared.ObjectID{0x21, 0x43, 0x65, 0x87, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab}); h.ParentID = nil }, ErrInvalidHeader},
		{"hard linked file with parent", func(h *Header) { h.Kind = ObjectKindHardLinkedFile }, ErrInvalidHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := sampleHeader()
			tt.change(&h)
			_, err := h.MarshalBinary()
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}

func TestHeaderUnmarshalRejectsInvalidLengthAndPadding(t *testing.T) {
	var h Header
	if err := h.UnmarshalBinary(make([]byte, HeaderSize-1)); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v", err)
	}
	b, err := sampleHeader().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	namePaddingOffset := 2 + 16 + 1 + 16 + 1 + 1 + 16 + 2 + len([]byte("メディア.bin"))
	b[namePaddingOffset] = 1
	if err := h.UnmarshalBinary(b); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v", err)
	}
}

func TestHeaderUnmarshalRejectsInvalidUUIDv4(t *testing.T) {
	data, err := sampleHeader().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data[2+6] = (data[2+6] & 0x0f) | 0x10
	var got Header
	if err := got.UnmarshalBinary(data); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func TestHeaderAllowsNullParentIDForRootAndHardLinkedFile(t *testing.T) {
	for _, kind := range []shared.ObjectKind{ObjectKindDirectory, ObjectKindHardLinkedFile} {
		t.Run(string(rune('0'+kind)), func(t *testing.T) {
			header := sampleHeader()
			header.Kind = kind
			header.ParentID = nil
			data, err := header.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			var got Header
			if err := got.UnmarshalBinary(data); err != nil {
				t.Fatal(err)
			}
			if got.ParentID != nil || got.Kind != kind {
				t.Fatalf("got ParentID %v, Kind %d", got.ParentID, got.Kind)
			}
		})
	}
}

func TestHeaderRejectsInvalidParentPresence(t *testing.T) {
	data, err := sampleHeader().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data[2+16] = 2
	var got Header
	if err := got.UnmarshalBinary(data); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func TestHeaderRejectsInvalidTargetPresence(t *testing.T) {
	header := sampleHeader()
	header.Kind = ObjectKindHardLink
	header.TargetID = ptrObjectID(shared.ObjectID{0x21, 0x43, 0x65, 0x87, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab})
	data, err := header.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	targetPresenceOffset := 2 + 16 + 1 + 16 + 1
	data[targetPresenceOffset] = 2
	var got Header
	if err := got.UnmarshalBinary(data); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func TestHeaderRejectsInvalidTargetUUID(t *testing.T) {
	header := sampleHeader()
	header.Kind = ObjectKindHardLink
	header.TargetID = ptrObjectID(shared.ObjectID{0x21, 0x43, 0x65, 0x87, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab})
	data, err := header.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	targetIDOffset := 2 + 16 + 1 + 16 + 1 + 1
	data[targetIDOffset+6] = (data[targetIDOffset+6] & 0x0f) | 0x10
	var got Header
	if err := got.UnmarshalBinary(data); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func TestHeaderRejectsInvalidParentUUID(t *testing.T) {
	data, err := sampleHeader().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data[2+16+1+6] = (data[2+16+1+6] & 0x0f) | 0x10
	var got Header
	if err := got.UnmarshalBinary(data); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func TestHeaderMarshalRejectsInvalidObjectID(t *testing.T) {
	header := sampleHeader()
	header.ObjectID = shared.ObjectID{}
	if _, err := header.MarshalBinary(); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func ptrObjectID(id shared.ObjectID) *shared.ObjectID {
	return &id
}

func equalOptionalObjectID(a, b *shared.ObjectID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}