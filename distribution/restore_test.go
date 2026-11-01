package distribution

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func testChunks(method distributionMethod, values map[chunkNo][]byte) []chunkDataWithInfo {
	key := stripeKeyInfo{
		objectID:      shared.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab},
		stripeID:      7,
		stripeVersion: 2,
	}
	result := make([]chunkDataWithInfo, 0, len(values))
	for no, data := range values {
		result = append(result, chunkDataWithInfo{
			stripeKey: key, distributionMethod: method, chunkNo: no, data: data,
		})
	}
	return result
}

func TestRestoreMirrorWithOneAvailableReplica(t *testing.T) {
	method := distributionMethod{distributionType: distributionMirror, numChunks: 3}
	got, err := restoreStripe(testChunks(method, map[chunkNo][]byte{2: []byte("mirror"), 0: []byte("mirror")}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("mirror")) {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreParityWithOneMissingDataChunk(t *testing.T) {
	method := distributionMethod{distributionType: distributionParity, numChunks: 3, numParities: 1}
	data := [][]byte{[]byte("abc"), []byte("def"), []byte("ghi")}
	parity := generateParity(data)
	got, err := restoreStripe(testChunks(method, map[chunkNo][]byte{0: data[0], 2: data[2], parityFlag: parity}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("abcdefghi")) {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreParityWithoutMissingChunk(t *testing.T) {
	method := distributionMethod{distributionType: distributionParity, numChunks: 2, numParities: 1}
	got, err := restoreStripe(testChunks(method, map[chunkNo][]byte{0: []byte("a"), 1: []byte("b")}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("ab")) {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreParityRejectsTwoMissingChunks(t *testing.T) {
	method := distributionMethod{distributionType: distributionParity, numChunks: 3, numParities: 1}
	_, err := restoreStripe(testChunks(method, map[chunkNo][]byte{0: []byte("a"), parityFlag: []byte{1}}))
	if !errors.Is(err, errTooManyMissingChunks) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreParityRejectsInconsistentParity(t *testing.T) {
	method := distributionMethod{distributionType: distributionParity, numChunks: 2, numParities: 1}
	_, err := restoreStripe(testChunks(method, map[chunkNo][]byte{0: []byte("a"), 1: []byte("b"), parityFlag: []byte("x")}))
	if !errors.Is(err, errInconsistentParity) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreRejectsMixedStripeVersions(t *testing.T) {
	method := distributionMethod{distributionType: distributionMirror, numChunks: 2}
	chunks := testChunks(method, map[chunkNo][]byte{0: []byte("x"), 1: []byte("x")})
	chunks[1].stripeKey.stripeVersion = storage.StripeVersion(3)
	if _, err := restoreStripe(chunks); !errors.Is(err, errInvalidChunk) {
		t.Fatalf("got %v", err)
	}
}

func TestChunkNoEncoding(t *testing.T) {
	data, err := newDataChunkNo(127)
	if err != nil {
		t.Fatal(err)
	}
	parity, err := newParityChunkNo(127)
	if err != nil {
		t.Fatal(err)
	}
	if data.isParity() || data.index() != 127 || !parity.isParity() || parity.index() != 127 {
		t.Fatalf("data=%08b parity=%08b", data, parity)
	}
}
