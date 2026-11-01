package distribution

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
		ObjectID: ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab},
		ParentID: ptrObjectID(ObjectID{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x4a, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0}),
		Kind: shared.ObjectKind(2),
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
	if got.ObjectID != want.ObjectID ||
		!equalOptionalObjectID(got.ParentID, want.ParentID) ||
		got.FormatVersion != want.FormatVersion ||
		got.Kind != want.Kind || got.Name != want.Name ||
		got.Mode != want.Mode || got.UID != want.UID ||
		got.GID != want.GID || got.Nlink != want.Nlink ||
		got.Size != want.Size || !got.Atime.Equal(want.Atime) ||
		!got.Mtime.Equal(want.Mtime) || !got.Ctime.Equal(want.Ctime) ||
		got.Layout != want.Layout {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestHeaderMarshalDefaultsVersion(t *testing.T) {
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
		{"version", func(h *Header) { h.FormatVersion = 2 }, ErrUnsupportedFormat},
		{"long name", func(h *Header) { h.Name = string(bytes.Repeat([]byte("x"), MaxNameSize+1)) }, ErrNameTooLong},
		{"invalid utf8", func(h *Header) { h.Name = string([]byte{0xff}) }, ErrInvalidName},
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
	b[2+16+1+16+1+2+len([]byte("メディア.bin"))] = 1
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

func TestHeaderAllowsNullParentID(t *testing.T) {
	header := sampleHeader()
	header.ParentID = nil

	data, err := header.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var got Header
	if err := got.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if got.ParentID != nil {
		t.Fatalf("got ParentID %v, want nil", got.ParentID)
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

func TestHeaderRejectsInvalidParentUUID(t *testing.T) {
	data, err := sampleHeader().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	// ParentID starts after version, ObjectID, and the presence marker.
	data[2+16+1+6] = (data[2+16+1+6] & 0x0f) | 0x10

	var got Header
	if err := got.UnmarshalBinary(data); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func TestHeaderMarshalRejectsInvalidObjectID(t *testing.T) {
	header := sampleHeader()
	header.ObjectID = ObjectID{}

	if _, err := header.MarshalBinary(); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("got %v, want ErrInvalidHeader", err)
	}
}

func ptrObjectID(id ObjectID) *ObjectID {
	return &id
}

func equalOptionalObjectID(a, b *ObjectID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
