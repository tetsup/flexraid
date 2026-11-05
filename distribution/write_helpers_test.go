package distribution

import (
	"bytes"
	"testing"
)

func TestValidateIOIndexes(t *testing.T) {
	tests := []struct {
		name    string
		indexes []int
		total   int
		minimum int
		wantErr string
	}{
		{name: "minimum satisfied", indexes: []int{0, 2}, total: 3, minimum: 2},
		{name: "more than minimum", indexes: []int{2, 0, 1}, total: 3, minimum: 1},
		{name: "empty allowed", indexes: []int{}, total: 0, minimum: 0},
		{name: "below minimum", indexes: []int{0}, total: 2, minimum: 2, wantErr: "insufficient"},
		{name: "negative index", indexes: []int{-1}, total: 2, minimum: 1, wantErr: "out of range"},
		{name: "index equals total", indexes: []int{2}, total: 2, minimum: 1, wantErr: "out of range"},
		{name: "index above total", indexes: []int{4}, total: 2, minimum: 1, wantErr: "out of range"},
		{name: "duplicate", indexes: []int{1, 1}, total: 2, minimum: 2, wantErr: "duplicate"},
		{name: "range checked after minimum", indexes: []int{}, total: 2, minimum: 1, wantErr: "insufficient"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIOIndexes(tt.indexes, tt.total, tt.minimum)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !containsText(err.Error(), tt.wantErr) {
				t.Fatalf("got error %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestSplitStripe(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		count uint8
		want  [][]byte
	}{
		{name: "empty payload", data: nil, count: 3, want: [][]byte{{}, {}, {}}},
		{name: "even split", data: []byte("abcdef"), count: 3, want: [][]byte{[]byte("ab"), []byte("cd"), []byte("ef")}},
		{name: "uneven split padded", data: []byte("abcde"), count: 3, want: [][]byte{[]byte("ab"), []byte("cd"), []byte("e ")}},
		{name: "smaller than chunk count", data: []byte("ab"), count: 4, want: [][]byte{[]byte("a"), []byte("b"), []byte(" "), []byte(" ")}},
		{name: "one chunk", data: []byte("abc"), count: 1, want: [][]byte{[]byte("abc")}},
		{name: "zero chunks", data: []byte("abc"), count: 0, want: [][]byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitStripe(tt.data, tt.count)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d chunks, want %d", len(got), len(tt.want))
			}
			for i, want := range tt.want {
				if !bytes.Equal(got[chunkNo(i)], want) {
					t.Errorf("chunk %d = %q, want %q", i, got[chunkNo(i)], want)
				}
			}
		})
	}
}

func TestOrderedDataChunks(t *testing.T) {
	chunks := map[chunkNo][]byte{
		2: []byte("third"),
		0: []byte("first"),
		1: []byte("second"),
	}
	got := orderedDataChunks(chunks, 3)
	want := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}
	got = orderedDataChunks(map[chunkNo][]byte{0: []byte("first")}, 3)
	if len(got) != 3 || got[1] != nil || got[2] != nil {
		t.Fatalf("missing map entries should remain nil, got %#v", got)
	}
}

func TestOrderedChunkNos(t *testing.T) {
	chunks := map[chunkNo][]byte{
		parityFlag | 1: []byte("parity1"),
		2:              []byte("data2"),
		0:              []byte("data0"),
		parityFlag:     []byte("parity0"),
		1:              []byte("data1"),
	}
	got := orderedChunkNos(chunks)
	want := []chunkNo{0, 1, 2, parityFlag, parityFlag | 1}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if gotEmpty := orderedChunkNos(nil); len(gotEmpty) != 0 {
		t.Fatalf("empty map returned %v", gotEmpty)
	}
}

func containsText(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

func TestSplitStripeWithExplicitSize(t *testing.T) {
	got := splitStripeWithSize([]byte("abcde"), 3, 2)
	want := [][]byte{[]byte("ab"), []byte("cd"), []byte{'e', 0}}
	for i := range want {
		if !bytes.Equal(got[chunkNo(i)], want[i]) {
			t.Fatalf("chunk %d: got %v want %v", i, got[chunkNo(i)], want[i])
		}
	}
}
