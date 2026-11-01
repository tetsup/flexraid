// Package shared contains types shared across filesystem layers.
package shared

import (
	"encoding/hex"
	"errors"
)

var ErrInvalidObjectID = errors.New("invalid object ID")

// ObjectID is the persistent identifier of a logical object.
// Zero is reserved as an invalid value.
type ObjectID uint64

// String returns the ObjectID as exactly 16 lowercase hexadecimal digits.
func (id ObjectID) String() string {
	return fmtHexUint64(uint64(id))
}

// ParseObjectID parses a 16-character hexadecimal ObjectID.
// Zero is rejected because it is reserved as an invalid value.
func ParseObjectID(value string) (ObjectID, error) {
	var id ObjectID
	if len(value) != 16 {
		return id, ErrInvalidObjectID
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 8 {
		return id, ErrInvalidObjectID
	}
	for _, b := range decoded {
		id = id<<8 | ObjectID(b)
	}
	if id == 0 {
		return 0, ErrInvalidObjectID
	}
	return id, nil
}

// ObjectKind identifies the kind of a logical filesystem object.
// Concrete values are defined by the layer that owns the object model.
type ObjectKind uint8

func fmtHexUint64(value uint64) string {
	const digits = "0123456789abcdef"
	var result [16]byte
	for i := len(result) - 1; i >= 0; i-- {
		result[i] = digits[value&0xf]
		value >>= 4
	}
	return string(result[:])
}
