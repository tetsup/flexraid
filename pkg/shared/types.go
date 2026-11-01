// Package shared contains types shared across filesystem layers.
package shared

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

var ErrInvalidObjectID = errors.New("invalid object ID")

// ObjectID is the persistent identifier of a logical object.
// It is an RFC 4122 UUID version 4.
type ObjectID [16]byte

// NewObjectID generates a UUIDv4 using cryptographically secure random bytes.
func NewObjectID() (ObjectID, error) {
	var id ObjectID
	if _, err := rand.Read(id[:]); err != nil {
		return ObjectID{}, err
	}

	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

// IsZero reports whether id is the zero value.
func (id ObjectID) IsZero() bool {
	return id == ObjectID{}
}

// String returns the canonical 36-character UUID representation.
func (id ObjectID) String() string {
	return hex.EncodeToString(id[0:4]) + "-" +
		hex.EncodeToString(id[4:6]) + "-" +
		hex.EncodeToString(id[6:8]) + "-" +
		hex.EncodeToString(id[8:10]) + "-" +
		hex.EncodeToString(id[10:16])
}

// ParseObjectID parses a canonical UUIDv4 string.
// The zero value and UUIDs that do not have the version 4 / RFC 4122 variant bits are rejected.
func ParseObjectID(value string) (ObjectID, error) {
	var id ObjectID
	if len(value) != 36 ||
		value[8] != '-' || value[13] != '-' ||
		value[18] != '-' || value[23] != '-' {
		return id, ErrInvalidObjectID
	}

	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(decoded) != len(id) {
		return id, ErrInvalidObjectID
	}
	copy(id[:], decoded)

	if id.IsZero() || id[6]>>4 != 4 || id[8]&0xc0 != 0x80 {
		return ObjectID{}, ErrInvalidObjectID
	}
	return id, nil
}

// ObjectKind identifies the kind of a logical filesystem object.
// Concrete values are defined by the layer that owns the object model.
type ObjectKind uint8
