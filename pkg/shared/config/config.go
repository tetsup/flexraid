// Package config contains shared storage defaults.
package config

const (
	DefaultDiskCount           = 4
	DefaultDataChunks          = 3
	DefaultParityChunks        = 1
	DefaultChunkSize    uint32 = 2 * 1024 * 1024
	MinimumChunkSize    uint32 = 4096
	HeaderReservedSize  uint64 = 1024
	HeaderDataCapacity  uint64 = 3072
)

type Placement struct {
	DiskCount    int
	DataChunks   int
	ParityChunks int
	MaxChunkSize uint32
}

func DefaultPlacement() Placement {
	return Placement{DiskCount: DefaultDiskCount, DataChunks: DefaultDataChunks, ParityChunks: DefaultParityChunks, MaxChunkSize: DefaultChunkSize}
}
