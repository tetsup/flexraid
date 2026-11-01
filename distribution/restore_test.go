package distribution

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
	"github.com/tetsup/flexraid/storage"
)

func testChunks(method DistributionMethod, values map[ChunkNo][]byte) []chunkDataWithInfo {
	key := StripeKeyInfo{
		ObjectID: shared.ObjectID{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab},
		StripeNo: 7,
		StripeVersion: 2,
	}
	result := make([]chunkDataWithInfo, 0, len(values))
	for no, data := range values {
		result = append(result, chunkDataWithInfo{StripeKey: key, DistributionMethod: method, ChunkNo: no, Data: data})
	}
	return result
}

func TestRestoreMirrorWithOneAvailableReplica(t *testing.T) {
	method := DistributionMethod{DistributionType: DistributionMirror, NumChunks: 3}
	got, err := restoreStripe(testChunks(method, map[ChunkNo][]byte{2: []byte("mirror"), 0: []byte("mirror")}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("mirror")) {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreParityWithOneMissingDataChunk(t *testing.T) {
	method := DistributionMethod{DistributionType: DistributionParity, NumChunks: 3, NumParities: 1}
	data := [][]byte{[]byte("abc"), []byte("def"), []byte("ghi")}
	parity := generateParity(data)
	got, err := restoreStripe(testChunks(method, map[ChunkNo][]byte{0: data[0], 2: data[2], ParityFlag: parity}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("abcdefghi")) {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreParityWithoutMissingChunk(t *testing.T) {
	method := DistributionMethod{DistributionType: DistributionParity, NumChunks: 2, NumParities: 1}
	got, err := restoreStripe(testChunks(method, map[ChunkNo][]byte{0: []byte("a"), 1: []byte("b")}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("ab")) {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreParityRejectsTwoMissingChunks(t *testing.T) {
	method := DistributionMethod{DistributionType: DistributionParity, NumChunks: 3, NumParities: 1}
	_, err := restoreStripe(testChunks(method, map[ChunkNo][]byte{0: []byte("a"), ParityFlag: []byte{1}}))
	if !errors.Is(err, ErrTooManyMissingChunks) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreParityRejectsInconsistentParity(t *testing.T) {
	method := DistributionMethod{DistributionType: DistributionParity, NumChunks: 2, NumParities: 1}
	_, err := restoreStripe(testChunks(method, map[ChunkNo][]byte{0: []byte("a"), 1: []byte("b"), ParityFlag: []byte("x")}))
	if !errors.Is(err, ErrInconsistentParity) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreRejectsMixedStripeVersions(t *testing.T) {
	method := DistributionMethod{DistributionType: DistributionMirror, NumChunks: 2}
	chunks := testChunks(method, map[ChunkNo][]byte{0: []byte("x"), 1: []byte("x")})
	chunks[1].StripeKey.StripeVersion = storage.StripeVersion(3)
	if _, err := restoreStripe(chunks); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("got %v", err)
	}
}

func TestChunkNoEncoding(t *testing.T) {
	data, err := NewDataChunkNo(127)
	if err != nil {
		t.Fatal(err)
	}
	parity, err := NewParityChunkNo(127)
	if err != nil {
		t.Fatal(err)
	}
	if data.IsParity() || data.Index() != 127 || !parity.IsParity() || parity.Index() != 127 {
		t.Fatalf("data=%08b parity=%08b", data, parity)
	}
}
