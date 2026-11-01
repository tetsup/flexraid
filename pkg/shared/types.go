// Package shared contains types shared across filesystem layers.
package shared

// ObjectKind identifies the kind of a logical filesystem object.
// Concrete values are defined by the layer that owns the object model.
type ObjectKind uint8
