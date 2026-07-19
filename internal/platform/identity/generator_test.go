package identity_test

import (
	"testing"

	"walletservice/internal/platform/identity"
)

func TestUUIDGenerator(t *testing.T) {
	t.Parallel()
	value, err := (identity.UUIDGenerator{}).New()
	if err != nil {
		t.Fatal(err)
	}
	if !identity.IsUUID(value) {
		t.Fatalf("generated value is not a UUID: %q", value)
	}
}

func TestIsUUIDRejectsMalformedValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-00000000000z"} {
		if identity.IsUUID(value) {
			t.Fatalf("accepted malformed UUID %q", value)
		}
	}
}
