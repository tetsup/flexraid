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
	HeaderSize                  = 2 + 16 + 1 + 16 + 1 + 1 + 16 + 2 + MaxNameSize + 4 + 4 + 4 + 4 + 8 + 8 + 8 + 8 + 1 + 4 + 2 + 2
)

var (
	ErrInvalidHeader     = errors.New("invalid header")
	ErrUnsupportedFormat = errors.New("unsupported header format")
	ErrNameTooLong       = errors.New("name too long")
	ErrInvalidName       = errors.New("invalid name")
)

// ObjectKind values identify the logical object types owned by Orchestration.
const (
	ObjectKindFile shared.ObjectKind = iota + 1
	ObjectKindDirectory
	ObjectKindSymlink
	ObjectKindHardLinkedFile
	ObjectKindHardLink
)

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
	ObjectID      shared.ObjectID
	ParentID      *shared.ObjectID
	TargetID      *shared.ObjectID
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
	if err := validateHeaderReferences(h.Kind, h.ParentID, h.TargetID); err != nil {
		return nil, err
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

	parentPresent, parentIDBytes, err := encodeOptionalObjectID(h.ParentID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ParentID: %v", ErrInvalidHeader, err)
	}
	targetPresent, targetIDBytes, err := encodeOptionalObjectID(h.TargetID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid TargetID: %v", ErrInvalidHeader, err)
	}

	b := bytes.NewBuffer(make([]byte, 0, HeaderSize))
	values := []any{h.FormatVersion, objectIDBytes, parentPresent, parentIDBytes, uint8(h.Kind), targetPresent, targetIDBytes, uint16(len(name))}
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
	var oidBytes, pidBytes, tidBytes [16]byte
	var parentPresent, targetPresent uint8
	var kind uint8
	var n uint16
	for _, v := range []any{&version, &oidBytes, &parentPresent, &pidBytes, &kind, &targetPresent, &tidBytes, &n} {
		if err := read(v); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHeader, err)
		}
	}
	if version != HeaderFormatVersion {
		return ErrUnsupportedFormat
	}

	var oid shared.ObjectID
	if err := oid.UnmarshalBinary(oidBytes[:]); err != nil {
		return fmt.Errorf("%w: invalid ObjectID: %v", ErrInvalidHeader, err)
	}
	parentID, err := decodeOptionalObjectID(parentPresent, pidBytes)
	if err != nil {
		return fmt.Errorf("%w: invalid ParentID: %v", ErrInvalidHeader, err)
	}
	targetID, err := decodeOptionalObjectID(targetPresent, tidBytes)
	if err != nil {
		return fmt.Errorf("%w: invalid TargetID: %v", ErrInvalidHeader, err)
	}
	objectKind := shared.ObjectKind(kind)
	if err := validateHeaderReferences(objectKind, parentID, targetID); err != nil {
		return err
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
	out.TargetID = targetID
	out.Kind = objectKind
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

func encodeOptionalObjectID(id *shared.ObjectID) (uint8, [16]byte, error) {
	var encoded [16]byte
	if id == nil {
		return 0, encoded, nil
	}
	data, err := id.MarshalBinary()
	if err != nil {
		return 0, encoded, err
	}
	copy(encoded[:], data)
	return 1, encoded, nil
}

func decodeOptionalObjectID(present uint8, encoded [16]byte) (*shared.ObjectID, error) {
	switch present {
	case 0:
		if encoded != [16]byte{} {
			return nil, ErrInvalidHeader
		}
		return nil, nil
	case 1:
		var id shared.ObjectID
		if err := id.UnmarshalBinary(encoded[:]); err != nil {
			return nil, err
		}
		return &id, nil
	default:
		return nil, ErrInvalidHeader
	}
}

func validateHeaderReferences(kind shared.ObjectKind, parentID, targetID *shared.ObjectID) error {
	switch kind {
	case ObjectKindFile, ObjectKindSymlink:
		if parentID == nil || targetID != nil {
			return ErrInvalidHeader
		}
	case ObjectKindDirectory:
		if targetID != nil {
			return ErrInvalidHeader
		}
	case ObjectKindHardLinkedFile:
		if parentID != nil || targetID != nil {
			return ErrInvalidHeader
		}
	case ObjectKindHardLink:
		if parentID == nil || targetID == nil {
			return ErrInvalidHeader
		}
	default:
		return ErrInvalidHeader
	}
	return nil
}