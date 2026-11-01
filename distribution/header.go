// Package distribution implements logical data distribution across storages.
package distribution

import (
	"errors"
	"time"

	"github.com/tetsup/flexraid/pkg/shared"
)

const (
	HeaderFormatVersion uint16 = 1

	MaxNameSize = 255

	// HeaderSize is the fixed on-disk size of a serialized Header.
	// It is independent of Go's in-memory struct layout.
	HeaderSize = 2 + // FormatVersion
		8 + // ObjectID
		8 + // ParentID
		1 + // Kind
		2 + // NameLen
		MaxNameSize + // Name
		4 + // Mode
		4 + // UID
		4 + // GID
		4 + // Nlink
		8 + // Size
		8 + // Atime
		8 + // Mtime
		8 + // Ctime
		1 + // Layout.Mode
		4 + // Layout.ChunkSize
		2 + // Layout.DataChunks
		2 // Layout.ParityChunks
)

var (
	ErrInvalidHeader     = errors.New("invalid header")
	ErrUnsupportedFormat = errors.New("unsupported header format")
	ErrNameTooLong       = errors.New("name too long")
	ErrInvalidName       = errors.New("invalid name")
)

// ObjectID is the shared persistent object identifier.
type ObjectID = shared.ObjectID

// LayoutMode identifies a physical data placement mode.
// Values are persisted and must not be changed after being assigned.
type LayoutMode uint8

const (
	LayoutModeMirror LayoutMode = iota + 1
	LayoutModeStripe
)

// Layout describes the physical chunk placement of an object.
type Layout struct {
	Mode         LayoutMode
	ChunkSize    uint32
	DataChunks   uint16
	ParityChunks uint16
}

// Header is the in-memory metadata for a logical filesystem object.
type Header struct {
	FormatVersion uint16

	ObjectID ObjectID
	ParentID ObjectID

	Kind shared.ObjectKind
	Name string

	Mode  uint32
	UID   uint32
	GID   uint32
	Nlink uint32

	Size uint64

	Atime time.Time
	Mtime time.Time
	Ctime time.Time

	Layout Layout
}
