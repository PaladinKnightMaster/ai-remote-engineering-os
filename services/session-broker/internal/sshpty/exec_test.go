package sshpty

import "testing"

func TestCappedBufferCapsAndFlags(t *testing.T) {
	c := &cappedBuffer{max: 5}
	if n, _ := c.Write([]byte("abc")); n != 3 || c.truncated {
		t.Fatalf("under cap: n=%d truncated=%v, want 3/false", n, c.truncated)
	}
	// "defgh" would overflow the 5-byte cap (3 used) → keep "de", flag truncated,
	// but report a full write so the copier is not blocked.
	if n, _ := c.Write([]byte("defgh")); n != 5 || !c.truncated {
		t.Fatalf("over cap: n=%d truncated=%v, want 5/true", n, c.truncated)
	}
	if got := string(c.Bytes()); got != "abcde" {
		t.Fatalf("captured = %q, want abcde", got)
	}
	// A further write past a full buffer is discarded but still reports full write.
	if n, _ := c.Write([]byte("zzz")); n != 3 {
		t.Fatalf("past-full write n=%d, want 3", n)
	}
	if got := string(c.Bytes()); got != "abcde" {
		t.Fatalf("captured after past-full = %q, want abcde", got)
	}
}

func TestCappedBufferUnbounded(t *testing.T) {
	c := &cappedBuffer{max: 0}
	if n, _ := c.Write([]byte("xxxxxxx")); n != 7 || c.truncated {
		t.Fatalf("unbounded: n=%d truncated=%v, want 7/false", n, c.truncated)
	}
	if got := string(c.Bytes()); got != "xxxxxxx" {
		t.Fatalf("captured = %q, want xxxxxxx", got)
	}
}
