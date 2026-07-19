package httpapi

import "testing"

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	encoded := encodeCursor(98765)
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != 98765 {
		t.Fatalf("expected 98765, got %d", decoded)
	}
}

func TestDecodeCursorRejectsInvalidValue(t *testing.T) {
	t.Parallel()
	if _, err := decodeCursor("not_base64!"); err == nil {
		t.Fatal("expected invalid cursor error")
	}
}
