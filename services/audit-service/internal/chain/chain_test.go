package chain

import (
	"testing"

	"aireos.local/services/audit-service/internal/audit"
)

func ev(id, idem string) audit.Event {
	return audit.Event{
		ID:             id,
		Timestamp:      "2026-05-20T00:00:00Z",
		Actor:          "svc:policy-service",
		Edge:           "service-service",
		Target:         "decision:42",
		Outcome:        "allow",
		IdempotencyKey: idem,
	}
}

// Compute is deterministic and order-sensitive on prevHash.
func TestComputeDeterministicAndChained(t *testing.T) {
	e := ev("01J0", "key-1")
	h1 := Compute(Genesis, e)
	h2 := Compute(Genesis, e)
	if h1 != h2 {
		t.Fatalf("Compute not deterministic: %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(h1))
	}
	// Linking the same event to a different prevHash must change the hash.
	if Compute("ff", e) == h1 {
		t.Fatal("chainHash did not depend on prevHash")
	}
}

// A tampered field after the fact breaks recomputation.
func TestTamperDetected(t *testing.T) {
	e := ev("01J0", "key-1")
	e.PrevHash = Genesis
	e.ChainHash = Compute(Genesis, e)

	tampered := e
	tampered.Target = "decision:99" // forged
	if Verify([]audit.Event{tampered}) != 0 {
		t.Fatal("expected Verify to flag the tampered event at index 0")
	}
}

// A well-formed multi-event chain verifies; a broken link is located.
func TestVerifyChain(t *testing.T) {
	var events []audit.Event
	prev := Genesis
	for _, idem := range []string{"k1", "k2", "k3"} {
		e := ev("id-"+idem, idem)
		e.PrevHash = prev
		e.ChainHash = Compute(prev, e)
		events = append(events, e)
		prev = e.ChainHash
	}
	if idx := Verify(events); idx != -1 {
		t.Fatalf("expected intact chain, broken at %d", idx)
	}

	// Splice: drop the middle event so event[2].prevHash no longer matches.
	broken := []audit.Event{events[0], events[2]}
	if idx := Verify(broken); idx != 1 {
		t.Fatalf("expected break at index 1, got %d", idx)
	}
}
