package storage

import "testing"

func sampleObjectID() ObjectID {
	return ObjectID(0x123456789abcdef0)
}

func TestObjectIDRoundTrip(t *testing.T) {
	want := sampleObjectID()
	got, err := ParseObjectID(want.String())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseObjectIDRejectsZero(t *testing.T) {
	if _, err := ParseObjectID("0000000000000000"); err == nil {
		t.Fatal("expected zero ObjectID to be rejected")
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
	const expected = "12/34/123456789abcdef0-0000000000001234-X5D2-0000000000005678"
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

func TestParseFileKeyRejectsWrongShard(t *testing.T) {
	_, err := ParseFileKey("ff/34/123456789abcdef0-0000000000001234-X5D2-0000000000005678")
	if err == nil {
		t.Fatal("expected invalid shard path error")
	}
}

func TestFileKeyPathRejectsZeroObjectID(t *testing.T) {
	key := FileKey{LayoutID: LayoutID{Type: 'X', DriveCount: 1, Role: ChunkRoleData, Index: 1}}
	if _, err := key.Path(); err == nil {
		t.Fatal("expected zero ObjectID to be rejected")
	}
}
