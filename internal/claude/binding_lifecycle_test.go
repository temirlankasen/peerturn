package claude

import (
	"testing"
	"time"
)

func TestBindingExpiredLeaseCannotBeRenewed(t *testing.T) {
	now := time.Now()
	b, err := NewBinding("11111111-1111-4111-8111-111111111111", "/tmp/claude.sock", "2.1.306", "gateway", now)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := b.Challenge(b.Socket, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Confirm(nonce, b.NativeSessionID, b.Instance, now); err != nil {
		t.Fatal(err)
	}
	if b.Ready(now.Add(21*time.Second)) || b.Touch(b.Instance, now.Add(21*time.Second)) == nil {
		t.Fatal("expired lease silently renewed")
	}
	if b.Ready(now.Add(22 * time.Second)) {
		t.Fatal("lost binding regained owner authority")
	}
}
