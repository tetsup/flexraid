package distribution

import (
 "errors"
 "fmt"
 "github.com/tetsup/flexraid/pkg/shared"
 "github.com/tetsup/flexraid/storage"
)

// ObjectID identifies a logical object whose data is distributed across stripes.
// It is shared with Orchestration, but Distribution does not interpret logical Header data.
type ObjectID = shared.ObjectID

type DistributionType uint8
const (
 DistributionMirror DistributionType = iota+1
 DistributionParity
)

// DistributionMethod describes the redundancy layout for one stripe.
// NumChunks is the replica count for Mirror and the data-chunk count for Parity.
type DistributionMethod struct {
 DistributionType DistributionType
 NumChunks uint8
 NumParities uint8
}

type ChunkNo uint8
const (
 ParityFlag ChunkNo = 0x80
 ChunkIndexMask ChunkNo = 0x7f
)
func (n ChunkNo) IsParity() bool { return n&ParityFlag!=0 }
func (n ChunkNo) Index() uint8 { return uint8(n&ChunkIndexMask) }
func NewDataChunkNo(index uint8)(ChunkNo,error){if index>uint8(ChunkIndexMask){return 0,fmt.Errorf("%w: chunk index out of range",ErrInvalidChunk)};return ChunkNo(index),nil}
func NewParityChunkNo(index uint8)(ChunkNo,error){if index>uint8(ChunkIndexMask){return 0,fmt.Errorf("%w: chunk index out of range",ErrInvalidChunk)};return ChunkNo(index)|ParityFlag,nil}

type StripeKeyInfo struct {
 ObjectID shared.ObjectID
 StripeNo storage.StripeID
 StripeVersion storage.StripeVersion
}
type chunkDataWithInfo struct {
 StripeKey StripeKeyInfo
 DistributionMethod DistributionMethod
 ChunkNo ChunkNo
 Data []byte
}
var (
 ErrInvalidDistributionMethod=errors.New("invalid distribution method")
 ErrInvalidChunk=errors.New("invalid chunk")
 ErrInsufficientChunks=errors.New("insufficient chunks to restore stripe")
 ErrTooManyMissingChunks=errors.New("too many missing data chunks")
 ErrInconsistentParity=errors.New("inconsistent parity data")
)
