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

func TestRestoreMirrorRejectsInconsistentReplicas(t *testing.T) {
	method := distributionMethod{distributionType: distributionMirror, numChunks: 3}
	_, err := restoreStripe(testChunks(method, map[chunkNo][]byte{0: []byte("good"), 2: []byte("bad")}))
	if !errors.Is(err, errInconsistentParity) {
		t.Fatalf("got %v, want errInconsistentParity", err)
	}
}

func TestValidateDistributionMethod(t *testing.T) {
	tests := []struct {
		name    string
		method  distributionMethod
		wantErr bool
	}{
		{
			name: "minimum mirror chunk count",
			method: distributionMethod{distributionType: distributionMirror, numChunks: 1},
		},
		{
			name: "maximum mirror chunk count",
			method: distributionMethod{distributionType: distributionMirror, numChunks: 127},
		},
		{
			name: "mirror rejects zero chunks",
			method: distributionMethod{distributionType: distributionMirror},
			wantErr: true,
		},
		{
			name: "mirror rejects more than 127 chunks",
			method: distributionMethod{distributionType: distributionMirror, numChunks: 128},
			wantErr: true,
		},
		{
			name: "mirror rejects parity chunks",
			method: distributionMethod{distributionType: distributionMirror, numChunks: 2, numParities: 1},
			wantErr: true,
		},
		{
			name: "minimum parity data chunk count",
			method: distributionMethod{distributionType: distributionParity, numChunks: 1, numParities: 1},
		},
		{
			name: "maximum parity data chunk count",
			method: distributionMethod{distributionType: distributionParity, numChunks: 127, numParities: 1},
		},
		{
			name: "parity rejects zero data chunks",
			method: distributionMethod{distributionType: distributionParity, numParities: 1},
			wantErr: true,
		},
		{
			name: "parity requires exactly one parity chunk",
			method: distributionMethod{distributionType: distributionParity, numChunks: 2},
			wantErr: true,
		},
		{
			name: "parity rejects multiple parity chunks",
			method: distributionMethod{distributionType: distributionParity, numChunks: 2, numParities: 2},
			wantErr: true,
		},
		{
			name: "unknown distribution type",
			method: distributionMethod{distributionType: distributionType(255), numChunks: 1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDistributionMethod(tt.method)
			if tt.wantErr {
				if !errors.Is(err, errInvalidDistributionMethod) {
					t.Fatalf("got error %v, want %v", err, errInvalidDistributionMethod)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateChunks(t *testing.T) {
	method := distributionMethod{distributionType: distributionParity, numChunks: 2, numParities: 1}
	valid := testChunks(method, map[chunkNo][]byte{
		0: []byte("first"),
		1: []byte("second"),
		parityFlag: []byte("parity"),
	})

	tests := []struct {
		name    string
		chunks  []chunkDataWithInfo
		method  distributionMethod
		wantErr bool
	}{
		{name: "valid data and parity chunks", chunks: valid, method: method},
		{name: "empty input", chunks: nil, method: method, wantErr: true},
		{name: "mixed stripe identity", chunks: func() []chunkDataWithInfo {
			chunks := append([]chunkDataWithInfo(nil), valid...)
			chunks[1].stripeKey.stripeVersion++
			return chunks
		}(), method: method, wantErr: true},
		{name: "mixed total lengths", chunks: func() []chunkDataWithInfo {
			chunks := append([]chunkDataWithInfo(nil), valid...)
			chunks[1].totalLength++
			return chunks
		}(), method: method, wantErr: true},
		{name: "mixed distribution methods", chunks: func() []chunkDataWithInfo {
			chunks := append([]chunkDataWithInfo(nil), valid...)
			chunks[1].distributionMethod.numChunks++
			return chunks
		}(), method: method, wantErr: true},
		{name: "duplicate chunk number", chunks: func() []chunkDataWithInfo {
			chunks := append([]chunkDataWithInfo(nil), valid...)
			chunks[1].chunkNo = chunks[0].chunkNo
			return chunks
		}(), method: method, wantErr: true},
		{name: "parity chunk in mirror method", chunks: testChunks(distributionMethod{distributionType: distributionMirror, numChunks: 2}, map[chunkNo][]byte{parityFlag: []byte("p")}), method: distributionMethod{distributionType: distributionMirror, numChunks: 2}, wantErr: true},
		{name: "parity index out of range", chunks: testChunks(method, map[chunkNo][]byte{parityFlag | 1: []byte("p")}), method: method, wantErr: true},
		{name: "data index out of range", chunks: testChunks(method, map[chunkNo][]byte{2: []byte("extra")}), method: method, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateChunks(tt.chunks, tt.method)
			if tt.wantErr {
				if !errors.Is(err, errInvalidChunk) {
					t.Fatalf("got error %v, want %v", err, errInvalidChunk)
				}
				if got != nil {
					t.Fatalf("got result %#v on invalid input, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.chunks) {
				t.Fatalf("got %d chunks, want %d", len(got), len(tt.chunks))
			}
		})
	}
}
