package shared

import "testing"

func TestObjectIDRoundTrip(t *testing.T) {
	const want ObjectID = 0x123456789abcdef0
	got, err := ParseObjectID(want.String())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseObjectIDRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{
		"",
		"123",
		"123456789abcdefg",
		"0000000000000000",
	} {
		if _, err := ParseObjectID(value); err == nil {
			t.Errorf("ParseObjectID(%q) succeeded, want error", value)
		}
	}
}
