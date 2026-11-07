// Package shared contains types shared across filesystem layers.
package shared

// ObjectKind identifies the kind of a logical filesystem object.
// Concrete values are defined by the layer that owns the object model.
type ObjectKind uint8

// Capacity reports physical filesystem capacity for one I/O root.
// All fields are expressed in bytes.
type Capacity struct {
	TotalBytes     uint64
	FreeBytes      uint64
	AvailableBytes uint64
}
