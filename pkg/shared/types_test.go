package shared

import "testing"

func TestObjectIDRoundTrip(t *testing.T) {
	want := ObjectID{
		0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef,
		0x8a, 0xbc, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab,
	}
	const text = "12345678-9abc-4def-8abc-1234567890ab"
	if got := want.String(); got != text {
		t.Fatalf("String() = %q, want %q", got, text)
	}
	got, err := ParseObjectID(text)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewObjectIDGeneratesUUIDv4(t *testing.T) {
	id, err := NewObjectID()
	if err != nil {
		t.Fatal(err)
	}
	if id.IsZero() {
		t.Fatal("NewObjectID returned zero")
	}
	if id[6]>>4 != 4 {
		t.Fatalf("version = %d, want 4", id[6]>>4)
	}
	if id[8]&0xc0 != 0x80 {
		t.Fatalf("variant bits = %02b, want 10xxxxxx", id[8]>>6)
	}
	if _, err := ParseObjectID(id.String()); err != nil {
		t.Fatalf("generated ID cannot be parsed: %v", err)
	}
}

func TestParseObjectIDRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{
		"",
		"123",
		"123456789abcdefg",
		"00000000-0000-0000-0000-000000000000",
		"12345678-9abc-1def-8abc-1234567890ab",
		"12345678-9abc-4def-0abc-1234567890ab",
		"123456789-abc-4def-8abc-1234567890ab",
	} {
		if _, err := ParseObjectID(value); err == nil {
			t.Errorf("ParseObjectID(%q) succeeded, want error", value)
		}
	}
}
