package transcriber

import "testing"

func TestDerefStr(t *testing.T) {
	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr(nil) = %q, want %q", got, "")
	}
	s := "hello"
	if got := derefStr(&s); got != "hello" {
		t.Errorf("derefStr(&%q) = %q, want %q", s, got, "hello")
	}
}

func TestDerefInt64(t *testing.T) {
	if got := derefInt64(nil); got != 0 {
		t.Errorf("derefInt64(nil) = %d, want 0", got)
	}
	n := int64(42)
	if got := derefInt64(&n); got != 42 {
		t.Errorf("derefInt64(&%d) = %d, want 42", n, got)
	}
}
