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

// NewObjectID generates a UUIDv4 using cryptographically secure randomness.
func NewObjectID() (ObjectID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ObjectID{}, err
	}

	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return parseObjectIDBytes(raw[:])
}

// ParseObjectID parses a canonical UUIDv4 string.
func ParseObjectID(value string) (ObjectID, error) {
	if len(value) != 36 ||
		value[8] != '-' || value[13] != '-' ||
		value[18] != '-' || value[23] != '-' {
		return ObjectID{}, ErrInvalidObjectID
	}

	raw, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil {
		return ObjectID{}, ErrInvalidObjectID
	}
	return parseObjectIDBytes(raw)
}

// UnmarshalBinary parses a UUIDv4 from its 16-byte binary representation.
func (id *ObjectID) UnmarshalBinary(data []byte) error {
	if id == nil {
		return ErrInvalidObjectID
	}
	parsed, err := parseObjectIDBytes(data)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// MarshalBinary returns the 16-byte binary representation.
func (id ObjectID) MarshalBinary() ([]byte, error) {
	return append([]byte(nil), id[:]...), nil
}

// String returns the canonical 36-character UUID representation.
func (id ObjectID) String() string {
	return hex.EncodeToString(id[0:4]) + "-" +
		hex.EncodeToString(id[4:6]) + "-" +
		hex.EncodeToString(id[6:8]) + "-" +
		hex.EncodeToString(id[8:10]) + "-" +
		hex.EncodeToString(id[10:16])
}

// IsZero reports whether id is the all-zero sentinel value.
func (id ObjectID) IsZero() bool {
	return id == ObjectID{}
}

// parseObjectIDBytes is the single UUIDv4 validation gate for all input formats.
func parseObjectIDBytes(data []byte) (ObjectID, error) {
	if len(data) != 16 {
		return ObjectID{}, ErrInvalidObjectID
	}
	var id ObjectID
	copy(id[:], data)
	if id[6]>>4 != 4 || id[8]&0xc0 != 0x80 {
		return ObjectID{}, ErrInvalidObjectID
	}
	return id, nil
}
