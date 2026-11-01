package distribution

import (
	"bytes"
	"fmt"
	"sort"
)

// restoreStripe validates chunk identity and dispatches to the selected layout.
func restoreStripe(chunks []chunkDataWithInfo) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, errInsufficientChunks
	}
	method := chunks[0].distributionMethod
	if err := validatedistributionMethod(method); err != nil {
		return nil, err
	}
	switch method.distributionType {
	case distributionMirror:
		return restoreMirror(chunks, method)
	case distributionParity:
		return restoreParity(chunks, method)
	default:
		return nil, ErrInvaliddistributionMethod
	}
}

func validatedistributionMethod(method distributionMethod) error {
	if method.numChunks == 0 || method.numChunks > 127 {
		return ErrInvaliddistributionMethod
	}
	switch method.distributionType {
	case distributionMirror:
		if method.numParities != 0 {
			return ErrInvaliddistributionMethod
		}
	case distributionParity:
		if method.numParities != 1 {
			return ErrInvaliddistributionMethod
		}
	default:
		return ErrInvaliddistributionMethod
	}
	return nil
}

func validateChunks(chunks []chunkDataWithInfo, method distributionMethod) (map[chunkNo][]byte, error) {
	var key StripeKeyInfo
	result := make(map[chunkNo][]byte, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			key = chunk.stripeKey
		} else if chunk.stripeKey != key {
			return nil, fmt.Errorf("%w: mixed stripe versions", errInvalidChunk)
		}
		if chunk.distributionMethod != method {
			return nil, fmt.Errorf("%w: mixed distribution methods", errInvalidChunk)
		}
		if _, exists := result[chunk.chunkNo]; exists {
			return nil, fmt.Errorf("%w: duplicate chunk number %d", errInvalidChunk, chunk.chunkNo)
		}
		if chunk.chunkNo.isParity() {
			if method.distributionType != distributionParity || chunk.chunkNo.index() >= method.numParities {
				return nil, fmt.Errorf("%w: parity chunk index %d out of range", errInvalidChunk, chunk.chunkNo.index())
			}
		} else if chunk.chunkNo.index() >= method.numChunks {
			return nil, fmt.Errorf("%w: data chunk index %d out of range", errInvalidChunk, chunk.chunkNo.index())
		}
		result[chunk.chunkNo] = chunk.data
	}
	return result, nil
}

func restoreMirror(chunks []chunkDataWithInfo, method distributionMethod) ([]byte, error) {
	if err := validatedistributionMethod(method); err != nil || method.distributionType != distributionMirror {
		return nil, ErrInvaliddistributionMethod
	}
	indexed, err := validateChunks(chunks, method)
	if err != nil {
		return nil, err
	}
	indexes := make([]int, 0, len(indexed))
	for no := range indexed {
		if no.isParity() {
			return nil, errInvalidChunk
		}
		indexes = append(indexes, int(no.index()))
	}
	if len(indexes) == 0 {
		return nil, errInsufficientChunks
	}
	sort.Ints(indexes)
	// Any available mirror replica contains the complete stripe.
	return append([]byte(nil), indexed[chunkNo(indexes[0])]...), nil
}

func restoreParity(chunks []chunkDataWithInfo, method distributionMethod) ([]byte, error) {
	if err := validatedistributionMethod(method); err != nil || method.distributionType != distributionParity {
		return nil, ErrInvaliddistributionMethod
	}
	indexed, err := validateChunks(chunks, method)
	if err != nil {
		return nil, err
	}
	data := make([][]byte, method.numChunks)
	missing := -1
	for i := range data {
		if value, ok := indexed[chunkNo(i)]; ok {
			data[i] = value
		} else if missing == -1 {
			missing = i
		} else {
			return nil, errTooManyMissingChunks
		}
	}
	if missing >= 0 {
		parity, ok := indexed[parityFlag]
		if !ok {
			return nil, errInsufficientChunks
		}
		restored := append([]byte(nil), parity...)
		for i, chunk := range data {
			if i == missing {
				continue
			}
			for j, b := range chunk {
				if j >= len(restored) {
					return nil, errInconsistentParity
				}
				restored[j] ^= b
			}
		}
		data[missing] = restored
	}
	// If all data chunks are present, parity is optional. If it is present,
	// verify it so corrupted parity is not silently accepted.
	if parity, ok := indexed[parityFlag]; ok {
		expected := generateParity(data)
		if !bytes.Equal(expected, parity) {
			return nil, errInconsistentParity
		}
	}
	return combineStripe(data), nil
}

func generateParity(chunks [][]byte) []byte {
	max := 0
	for _, chunk := range chunks {
		if len(chunk) > max {
			max = len(chunk)
		}
	}
	parity := make([]byte, max)
	for _, chunk := range chunks {
		for i, b := range chunk {
			parity[i] ^= b
		}
	}
	return parity
}

func combineStripe(chunks [][]byte) []byte {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	result := make([]byte, 0, total)
	for _, chunk := range chunks {
		result = append(result, chunk...)
	}
	return result
}
