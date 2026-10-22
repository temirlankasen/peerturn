package fault

import (
	"fmt"
	"testing"
)

func TestStableCodeAndWrapping(t *testing.T) {
	err := New(InvalidSignature)
	if err.Error() != "invalid_signature" || !IsCode(fmt.Errorf("wrapped: %w", err), InvalidSignature) {
		t.Fatal("stable code lost")
	}
	if IsCode(err, InvalidEncoding) || IsCode(nil, InvalidSignature) {
		t.Fatal("unrelated error matched")
	}
}
