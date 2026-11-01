package orchestration

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/tetsup/flexraid/pkg/shared"
)

const (
	HeaderFormatVersion uint16 = 1
	MaxNameSize                 = 255
	HeaderSize                  = 2 + 16 + 1 + 16 + 1 + 2 + MaxNameSize + 4 + 4 + 4 + 4 + 8 + 8 + 8 + 8 + 1 + 4 + 2 + 2
)

var (
	ErrInvalidHeader     = errors.New("invalid header")
	ErrUnsupportedFormat = errors.New("unsupported header format")
	ErrNameTooLong       = errors.New("name too long")
	ErrInvalidName       = errors.New("invalid name")
)

type ObjectID = shared.ObjectID

type LayoutMode uint8

const (
	LayoutModeMirror LayoutMode = iota + 1
	LayoutModeStripe
)

type Layout struct {
	Mode         LayoutMode
	ChunkSize    uint32
	DataChunks   uint16
	ParityChunks uint16
}

type Header struct {
	FormatVersion uint16
	ObjectID      ObjectID
	ParentID      *ObjectID
	Kind          shared.ObjectKind
	Name          string
	Mode          uint32
	UID           uint32
	GID           uint32
	Nlink         uint32
	Size          uint64
	Atime         time.Time
	Mtime         time.Time
	Ctime         time.Time
	Layout        Layout
}

// MarshalBinary encodes a logical object header as fixed-size, big-endian bytes.
// UUIDs are stored as 16 raw bytes and timestamps use Unix nanoseconds.
func (h Header) MarshalBinary() ([]byte, error) {
	if h.FormatVersion == 0 {
		h.FormatVersion = HeaderFormatVersion
	}
	if h.FormatVersion != HeaderFormatVersion {
		return nil, ErrUnsupportedFormat
	}
	name := []byte(h.Name)
	if len(name) > MaxNameSize {
		return nil, ErrNameTooLong
	}
	if !utf8.Valid(name) {
		return nil, ErrInvalidName
	}
	if h.Layout.Mode != LayoutModeMirror && h.Layout.Mode != LayoutModeStripe {
		return nil, fmt.Errorf("%w: invalid layout mode", ErrInvalidHeader)
	}

	objectIDBytes, err := h.ObjectID.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ObjectID: %v", ErrInvalidHeader, err)
	}

	var parentPresent uint8
	var parentIDBytes [16]byte
	if h.ParentID != nil {
		parentPresent = 1
		encoded, err := h.ParentID.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("%w: invalid ParentID: %v", ErrInvalidHeader, err)
		}
		copy(parentIDBytes[:], encoded)
	}

	b := bytes.NewBuffer(make([]byte, 0, HeaderSize))
	values := []any{h.FormatVersion, objectIDBytes, parentPresent, parentIDBytes, uint8(h.Kind), uint16(len(name))}
	for _, v := range values {
		if err := binary.Write(b, binary.BigEndian, v); err != nil {
			return nil, err
		}
	}
	b.Write(name)
	b.Write(make([]byte, MaxNameSize-len(name)))
	values = []any{h.Mode, h.UID, h.GID, h.Nlink, h.Size, h.Atime.UnixNano(), h.Mtime.UnixNano(), h.Ctime.UnixNano(), uint8(h.Layout.Mode), h.Layout.ChunkSize, h.Layout.DataChunks, h.Layout.ParityChunks}
	for _, v := range values {
		if err := binary.Write(b, binary.BigEndian, v); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// UnmarshalBinary decodes a logical object header from fixed-size, big-endian bytes.
func (h *Header) UnmarshalBinary(data []byte) error {
	if h == nil || len(data) != HeaderSize {
		return ErrInvalidHeader
	}
	r := bytes.NewReader(data)
	read := func(v any) error { return binary.Read(r, binary.BigEndian, v) }
	var version uint16
	var oidBytes, pidBytes [16]byte
	var parentPresent uint8
	var kind uint8
	var n uint16
	for _, v := range []any{&version, &oidBytes, &parentPresent, &pidBytes, &kind, &n} {
		if err := read(v); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHeader, err)
		}
	}
	if version != HeaderFormatVersion {
		return ErrUnsupportedFormat
	}

	var oid, pid shared.ObjectID
	if err := oid.UnmarshalBinary(oidBytes[:]); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidHeader, err)
	}
	var parentID *shared.ObjectID
	switch parentPresent {
	case 0:
		// The presence marker is authoritative. The reserved ParentID bytes
		// must be canonical padding when no parent is present.
		if pidBytes != [16]byte{} {
			return ErrInvalidHeader
		}
	case 1:
		if err := pid.UnmarshalBinary(pidBytes[:]); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHeader, err)
		}
		parentID = &pid
	default:
		return ErrInvalidHeader
	}
	if n > MaxNameSize {
		return ErrInvalidHeader
	}

	namebuf := make([]byte, MaxNameSize)
	if _, err := r.Read(namebuf); err != nil {
		return ErrInvalidHeader
	}
	name := namebuf[:n]
	if !utf8.Valid(name) {
		return ErrInvalidName
	}
	for _, v := range namebuf[n:] {
		if v != 0 {
			return ErrInvalidHeader
		}
	}
	var out Header
	out.FormatVersion = version
	out.ObjectID = oid
	out.ParentID = parentID
	out.Kind = shared.ObjectKind(kind)
	out.Name = string(name)
	var at, mt, ct int64
	var lm uint8
	for _, v := range []any{&out.Mode, &out.UID, &out.GID, &out.Nlink, &out.Size, &at, &mt, &ct, &lm, &out.Layout.ChunkSize, &out.Layout.DataChunks, &out.Layout.ParityChunks} {
		if err := read(v); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHeader, err)
		}
	}
	out.Atime = time.Unix(0, at)
	out.Mtime = time.Unix(0, mt)
	out.Ctime = time.Unix(0, ct)
	out.Layout.Mode = LayoutMode(lm)
	if lm != uint8(LayoutModeMirror) && lm != uint8(LayoutModeStripe) {
		return ErrInvalidHeader
	}
	*h = out
	return nil
}
