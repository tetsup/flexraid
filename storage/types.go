package storage

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tetsup/flexraid/pkg/shared"
)

var (
	ErrInvalidLayoutID = errors.New("invalid layout ID")
	ErrInvalidFileKey  = errors.New("invalid file key")
)

// StripeVersion identifies one version of a stripe.
type StripeVersion uint64

// StripeID identifies a stripe within an object.
type StripeID uint64

// LayoutType identifies a distribution scheme.
type LayoutType byte

// ChunkRole identifies a chunk's role within a layout.
type ChunkRole byte

const (
	ChunkRoleData   ChunkRole = 'D'
	ChunkRoleParity ChunkRole = 'P'
)

// LayoutID describes the placement of one chunk.
//
// Its four-character representation consists of the scheme, drive count,
// chunk role, and ordinal within that role, in that order.
type LayoutID struct {
	Type       LayoutType
	DriveCount uint8
	Role       ChunkRole
	Index      uint8
}

// String returns the four-character representation of the layout ID.
// The current single-character numeric fields support values 1 through 9.
func (id LayoutID) String() (string, error) {
	if !isUpperAlpha(byte(id.Type)) || id.DriveCount == 0 || id.DriveCount > 9 ||
		(id.Role != ChunkRoleData && id.Role != ChunkRoleParity) ||
		id.Index == 0 || id.Index > 9 {
		return "", ErrInvalidLayoutID
	}

	return string([]byte{byte(id.Type), '0' + id.DriveCount, byte(id.Role), '0' + id.Index}), nil
}

// ParseLayoutID parses a four-character layout ID.
func ParseLayoutID(value string) (LayoutID, error) {
	if len(value) != 4 {
		return LayoutID{}, ErrInvalidLayoutID
	}
	b := []byte(value)
	if !isUpperAlpha(b[0]) || b[1] < '1' || b[1] > '9' ||
		(b[2] != byte(ChunkRoleData) && b[2] != byte(ChunkRoleParity)) ||
		b[3] < '1' || b[3] > '9' {
		return LayoutID{}, ErrInvalidLayoutID
	}

	return LayoutID{
		Type:       LayoutType(b[0]),
		DriveCount: b[1] - '0',
		Role:       ChunkRole(b[2]),
		Index:      b[3] - '0',
	}, nil
}

func isUpperAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z'
}

// FileKey identifies one physical chunk file.
type FileKey struct {
	ObjectID      shared.ObjectID
	StripeVersion StripeVersion
	LayoutID      LayoutID
	StripeID      StripeID
}

// Path returns the relative path for this key. The I/O layer supplies the root.
func (key FileKey) Path() (string, error) {
	layout, err := key.LayoutID.String()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFileKey, err)
	}

	objectID := key.ObjectID.String()
	name := fmt.Sprintf("%s-%016x-%s-%016x",
		objectID, uint64(key.StripeVersion), layout, uint64(key.StripeID))
	return objectID[:2] + "/" + objectID[2:4] + "/" + name, nil
}

// ParseFileKey parses the relative path returned by FileKey.Path.
func ParseFileKey(path string) (FileKey, error) {
	var key FileKey
	parts := strings.Split(path, "/")
	if len(parts) != 3 {
		return key, ErrInvalidFileKey
	}

	name := parts[2]
	const expectedLength = 36 + 1 + 16 + 1 + 4 + 1 + 16
	if len(name) != expectedLength {
		return key, ErrInvalidFileKey
	}

	objectIDText := name[:36]
	if parts[0] != objectIDText[:2] || parts[1] != objectIDText[2:4] {
		return key, ErrInvalidFileKey
	}

	if name[36] != '-' || name[53] != '-' || name[58] != '-' {
		return key, ErrInvalidFileKey
	}
	objectID, err := shared.ParseObjectID(objectIDText)
	if err != nil {
		return key, fmt.Errorf("%w: %v", ErrInvalidFileKey, err)
	}
	version, err := strconv.ParseUint(name[37:53], 16, 64)
	if err != nil {
		return key, ErrInvalidFileKey
	}
	layout, err := ParseLayoutID(name[54:58])
	if err != nil {
		return key, ErrInvalidFileKey
	}
	stripeID, err := strconv.ParseUint(name[59:75], 16, 64)
	if err != nil {
		return key, ErrInvalidFileKey
	}

	return FileKey{
		ObjectID:      objectID,
		StripeVersion: StripeVersion(version),
		LayoutID:      layout,
		StripeID:      StripeID(stripeID),
	}, nil
}
