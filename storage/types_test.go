package storage

import (
	"testing"

	"github.com/tetsup/flexraid/pkg/shared"
)

func sampleObjectID() shared.ObjectID {
	return shared.ObjectID{
		0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef,
		0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab,
	}
}

func TestLayoutIDRoundTrip(t *testing.T) {
	want := LayoutID{Type: 'X', DriveCount: 5, Role: ChunkRoleParity, Index: 1}
	text, err := want.String()
	if err != nil {
		t.Fatal(err)
	}
	if text != "X5P1" {
		t.Fatalf("got %q, want X5P1", text)
	}
	got, err := ParseLayoutID(text)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestFileKeyPathRoundTrip(t *testing.T) {
	want := FileKey{
		ObjectID:      sampleObjectID(),
		StripeVersion: StripeVersion(0x1234),
		LayoutID:      LayoutID{Type: 'X', DriveCount: 5, Role: ChunkRoleData, Index: 2},
		StripeID:      StripeID(0x5678),
	}
	path, err := want.Path()
	if err != nil {
		t.Fatal(err)
	}
	const expected = "12/34/12345678-9abc-4def-8abc-1234567890ab-0000000000001234-X5D2-0000000000005678"
	if path != expected {
		t.Fatalf("got %q, want %q", path, expected)
	}
	got, err := ParseFileKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseFileKeyRejectsInvalidObjectID(t *testing.T) {
	_, err := ParseFileKey("12/34/12345678-9abc-1def-8abc-1234567890ab-0000000000001234-X5D2-0000000000005678")
	if err == nil {
		t.Fatal("expected invalid ObjectID error")
	}
}

func TestParseFileKeyRejectsWrongShard(t *testing.T) {
	_, err := ParseFileKey("ff/34/12345678-9abc-4def-8abc-1234567890ab-0000000000001234-X5D2-0000000000005678")
	if err == nil {
		t.Fatal("expected invalid shard path error")
	}
}
