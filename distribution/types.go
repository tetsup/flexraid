package distribution

import (
	"errors"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type distributionType uint8

const (
	distributionMirror distributionType = iota + 1
	distributionParity
)

// distributionMethod describes the redundancy layout for one stripe.
// numChunks is the replica count for Mirror and the data-chunk count for Parity.
type distributionMethod struct {
	distributionType distributionType
	numChunks        uint8
	numParities      uint8
}

type chunkNo uint8

const (
	parityFlag      chunkNo = 0x80
	chunkIndexMask  chunkNo = 0x7f
)

func (n chunkNo) isParity() bool { return n&parityFlag != 0 }
func (n chunkNo) index() uint8  { return uint8(n & chunkIndexMask) }

func newDataChunkNo(index uint8) (chunkNo, error) {
	if index > uint8(chunkIndexMask) {
		return 0, fmt.Errorf("%w: chunk index out of range", errInvalidChunk)
	}
	return chunkNo(index), nil
}

func newParityChunkNo(index uint8) (chunkNo, error) {
	if index > uint8(chunkIndexMask) {
		return 0, fmt.Errorf("%w: chunk index out of range", errInvalidChunk)
	}
	return chunkNo(index) | parityFlag, nil
}

type stripeKeyInfo struct {
	objectID      shared.ObjectID
	stripeID      storage.StripeID
	stripeVersion storage.StripeVersion
}

type chunkDataWithInfo struct {
	stripeKey         stripeKeyInfo
	distributionMethod distributionMethod
	chunkNo            chunkNo
	data               []byte
}

var (
	errInvalidDistributionMethod = errors.New("invalid distribution method")
	errInvalidChunk              = errors.New("invalid chunk")
	errInsufficientChunks        = errors.New("insufficient chunks to restore stripe")
	errTooManyMissingChunks      = errors.New("too many missing data chunks")
	errInconsistentParity        = errors.New("inconsistent parity data")
)
