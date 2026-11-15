package io

// RootPath returns the configured root directory for this I/O layer.
func (layer *IO) RootPath() string {
	return layer.root
}
