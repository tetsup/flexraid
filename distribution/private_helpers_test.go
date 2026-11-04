package distribution

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/tetsup/flexraid/storage"
)

func TestDecodeChunk(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantLen uint64
		want    []byte
		wantErr bool
	}{
		{name: "empty", data: nil, wantErr: true},
		{name: "short length prefix", data: []byte{1, 2, 3, 4, 5, 6, 7}, wantErr: true},
		{name: "zero length payload", data: make([]byte, 8), wantLen: 0, want: []byte{}},
		{name: "payload", data: append(binary.BigEndian.AppendUint64(nil, 5), []byte("hello")...), wantLen: 5, want: []byte("hello")},
		{name: "length differs from payload size", data: append(binary.BigEndian.AppendUint64(nil, 99), []byte("x")...), wantLen: 99, want: []byte("x")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLen, got, err := decodeChunk(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if got != nil {
					t.Fatalf("got payload %q on error, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotLen != tt.wantLen {
				t.Fatalf("got total length %d, want %d", gotLen, tt.wantLen)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("got payload %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeChunkReturnsPayloadCopy(t *testing.T) {
	data := append(binary.BigEndian.AppendUint64(nil, 3), []byte("abc")...)
	_, got, err := decodeChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	got[0] = 'x'
	if !bytes.Equal(data[8:], []byte("abc")) {
		t.Fatalf("decoded payload aliases input: %q", data[8:])
	}
}

func TestCloneChunks(t *testing.T) {
	source := map[chunkNo]chunkLocation{
		0: {ioIndex: 2, key: storage.FileKey{StripeVersion: 7}, data: []byte("alpha"), totalLength: 5},
		parityFlag: {ioIndex: 3, key: storage.FileKey{StripeVersion: 8}, data: []byte("parity"), totalLength: 5},
	}
	got := cloneChunks(source)
	if len(got) != len(source) {
		t.Fatalf("got %d entries, want %d", len(got), len(source))
	}
	for no, want := range source {
		actual, ok := got[no]
		if !ok {
			t.Fatalf("missing chunk %v", no)
		}
		if actual.ioIndex != want.ioIndex || actual.key != want.key || actual.totalLength != want.totalLength || !bytes.Equal(actual.data, want.data) {
			t.Fatalf("chunk %v got %#v, want %#v", no, actual, want)
		}
	}
	got[0].data[0] = 'X'
	if !bytes.Equal(source[0].data, []byte("alpha")) {
		t.Fatalf("cloned payload aliases source: %q", source[0].data)
	}
	got[0] = chunkLocation{}
	if source[0].ioIndex != 2 {
		t.Fatalf("replacing clone entry changed source: %#v", source[0])
	}
}

func TestCurrentVersion(t *testing.T) {
	tests := []struct {
		name    string
		info    *stripeInfo
		version storage.StripeVersion
		want    bool
	}{
		{name: "nil current stripe", version: 1, want: false},
		{name: "matches", info: &stripeInfo{version: 4}, version: 4, want: true},
		{name: "different version", info: &stripeInfo{version: 4}, version: 3, want: false},
		{name: "zero version match", info: &stripeInfo{}, version: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := currentVersion(tt.info, tt.version); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpectedChunkNos(t *testing.T) {
	tests := []struct {
		name   string
		method distributionMethod
		want   []chunkNo
	}{
		{
			name: "mirror data chunks",
			method: distributionMethod{distributionType: distributionMirror, numChunks: 3},
			want: []chunkNo{0, 1, 2},
		},
		{
			name: "parity data and parity chunk",
			method: distributionMethod{distributionType: distributionParity, numChunks: 2, numParities: 1},
			want: []chunkNo{0, 1, parityFlag},
		},
		{
			name: "multiple parity indexes",
			method: distributionMethod{distributionType: distributionParity, numChunks: 1, numParities: 2},
			want: []chunkNo{0, parityFlag, parityFlag | 1},
		},
		{
			name: "empty method",
			method: distributionMethod{},
			want: []chunkNo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expectedChunkNos(tt.method)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
