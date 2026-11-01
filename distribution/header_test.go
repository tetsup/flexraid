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
		ParentID: ObjectID{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x4a, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0},
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
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestHeaderMarshalDefaultsVersion(t *testing.T) {
	h := sampleHeader()
	h.FormatVersion = 0
	b, e := h.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	var got Header
	if e = got.UnmarshalBinary(b); e != nil {
		t.Fatal(e)
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
		{"zero ID", func(h *Header) { h.ObjectID = ObjectID{} }, ErrInvalidHeader},
		{"version", func(h *Header) { h.FormatVersion = 2 }, ErrUnsupportedFormat},
		{"long name", func(h *Header) { h.Name = string(bytes.Repeat([]byte("x"), MaxNameSize+1)) }, ErrNameTooLong},
		{"invalid utf8", func(h *Header) { h.Name = string([]byte{0xff}) }, ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := sampleHeader()
			tt.change(&h)
			_, e := h.MarshalBinary()
			if !errors.Is(e, tt.want) {
				t.Fatalf("got %v want %v", e, tt.want)
			}
		})
	}
}

func TestHeaderUnmarshalRejectsInvalidLengthAndPadding(t *testing.T) {
	var h Header
	if e := h.UnmarshalBinary(make([]byte, HeaderSize-1)); !errors.Is(e, ErrInvalidHeader) {
		t.Fatalf("got %v", e)
	}
	b, e := sampleHeader().MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	b[2+16+16+1+2+len([]byte("メディア.bin"))] = 1
	if e = h.UnmarshalBinary(b); !errors.Is(e, ErrInvalidHeader) {
		t.Fatalf("got %v", e)
	}
}
