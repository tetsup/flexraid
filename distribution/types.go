package distribution

import (
	"errors"
	"fmt"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

type LayoutType uint8

const (
	LayoutMirror LayoutType = 'M'
	LayoutParity LayoutType = 'P'
)

// Layout describes the redundancy scheme for one logical stripe.
// NumChunks is the replica count for Mirror and data chunk count for XOR Parity.
type Layout struct {
	Type LayoutType
	NumChunks uint8
}

type distributionType uint8

const (
	distributionMirror distributionType = iota + 1
	distributionParity
)

type distributionMethod struct {
	distributionType distributionType
	numChunks uint8
	numParities uint8
}

func (layout Layout) method() (distributionMethod, error) {
	if layout.NumChunks == 0 || (layout.Type == LayoutMirror && layout.NumChunks > 9) || (layout.Type == LayoutParity && layout.NumChunks > 8) {
		return distributionMethod{}, errInvalidDistributionMethod
	}
	switch layout.Type {
	case LayoutMirror:
		return distributionMethod{distributionType: distributionMirror, numChunks: layout.NumChunks}, nil
	case LayoutParity:
		return distributionMethod{distributionType: distributionParity, numChunks: layout.NumChunks, numParities: 1}, nil
	default:
		return distributionMethod{}, errInvalidDistributionMethod
	}
}

func (method distributionMethod) layout() Layout {
	if method.distributionType == distributionMirror {
		return Layout{Type: LayoutMirror, NumChunks: method.numChunks}
	}
	return Layout{Type: LayoutParity, NumChunks: method.numChunks}
}

type chunkNo uint8

const (
	parityFlag chunkNo = 0x80
	chunkIndexMask chunkNo = 0x7f
)

func (n chunkNo) isParity() bool { return n&parityFlag != 0 }
func (n chunkNo) index() uint8 { return uint8(n & chunkIndexMask) }

func newDataChunkNo(index uint8) (chunkNo, error) {
	if index > uint8(chunkIndexMask) { return 0, fmt.Errorf("%w: chunk index out of range", errInvalidChunk) }
	return chunkNo(index), nil
}

func newParityChunkNo(index uint8) (chunkNo, error) {
	if index > uint8(chunkIndexMask) { return 0, fmt.Errorf("%w: chunk index out of range", errInvalidChunk) }
	return chunkNo(index) | parityFlag, nil
}

type stripeKeyInfo struct {
	objectID shared.ObjectID
	stripeID storage.StripeID
	stripeVersion storage.StripeVersion
}

type chunkDataWithInfo struct {
	stripeKey stripeKeyInfo
	distributionMethod distributionMethod
	chunkNo chunkNo
	data []byte
	totalLength uint64
}

var (
	errInvalidDistributionMethod = errors.New("invalid distribution method")
	errInvalidChunk = errors.New("invalid chunk")
	errInsufficientChunks = errors.New("insufficient chunks to restore stripe")
	errTooManyMissingChunks = errors.New("too many missing data chunks")
	errInconsistentParity = errors.New("inconsistent parity data")
	errDataCorrupted = errors.New("stripe data corrupted")
	errStripeNotFound = errors.New("stripe not found")
	errNoUsableIO = errors.New("no usable I/O")
)
