package verify

import (
	"crypto/ed25519"
	"fmt"
	"testing"

	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return pub, sk
}

func ev(seq int64, idem string) audit.Event {
	return audit.Event{
		Seq:            seq,
		ID:             "id-" + idem,
		Timestamp:      "2026-08-12T00:00:00Z",
		Actor:          "svc:policy-service",
		Edge:           "service-service",
		Target:         "decision:" + idem,
		Outcome:        "allow",
		IdempotencyKey: idem,
	}
}

// buildChain returns n events (seq 1..n) forming a valid chain from Genesis,
// distinguished by the salt so two independently built chains never collide.
func buildChain(n int, salt string) []audit.Event {
	var events []audit.Event
	prev := chain.Genesis
	for i := 1; i <= n; i++ {
		e := ev(int64(i), fmt.Sprintf("%s-k%d", salt, i))
		e.PrevHash = prev
		e.ChainHash = chain.Compute(prev, e)
		events = append(events, e)
		prev = e.ChainHash
	}
	return events
}

func chainHashAt(events []audit.Event, seq int64) string {
	for _, e := range events {
		if e.Seq == seq {
			return e.ChainHash
		}
	}
	return ""
}

func signHeadAt(t *testing.T, sk ed25519.PrivateKey, events []audit.Event, seq int64) chain.Head {
	t.Helper()
	h, err := chain.SignHead(sk, seq, chainHashAt(events, seq), "2026-08-12T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign head at seq %d: %v", seq, err)
	}
	return h
}

// flipHexChar mutates the hex string just enough to make it decode to a
// different value, without changing its length — used to corrupt a
// signature while keeping it syntactically valid hex.
func flipHexChar(s string) string {
	b := []byte(s)
	if b[0] == '0' {
		b[0] = '1'
	} else {
		b[0] = '0'
	}
	return string(b)
}

// ---- OutcomeIntactAttested ----

func TestChainAttestedWhenLastEventIsCoveredByAVerifiedHead(t *testing.T) {
	pub, sk := testKeypair(t)
	events := buildChain(5, "a")
	head := signHeadAt(t, sk, events, 5)

	got := Chain(events, []chain.Head{head}, NewKeyRing(pub))

	if got.Outcome != OutcomeIntactAttested {
		t.Fatalf("Outcome = %v, want OutcomeIntactAttested", got.Outcome)
	}
	if got.AttestedThroughSeq != 5 {
		t.Fatalf("AttestedThroughSeq = %d, want 5", got.AttestedThroughSeq)
	}
	if got.EventsVerified != 5 {
		t.Fatalf("EventsVerified = %d, want 5", got.EventsVerified)
	}
}

// ---- OutcomeIntactUnattested ----

func TestChainUnattestedWhenTailIsBeyondTheLastHead(t *testing.T) {
	pub, sk := testKeypair(t)
	events := buildChain(5, "b")
	head := signHeadAt(t, sk, events, 3) // checkpoint hasn't caught up to seq 5 yet

	got := Chain(events, []chain.Head{head}, NewKeyRing(pub))

	if got.Outcome != OutcomeIntactUnattested {
		t.Fatalf("Outcome = %v, want OutcomeIntactUnattested", got.Outcome)
	}
	if got.AttestedThroughSeq != 3 {
		t.Fatalf("AttestedThroughSeq = %d, want 3", got.AttestedThroughSeq)
	}
	if got.UnattestedFromSeq != 4 {
		t.Fatalf("UnattestedFromSeq = %d, want 4", got.UnattestedFromSeq)
	}
}

func TestChainUnattestedWhenNoHeadExistsAtAll(t *testing.T) {
	events := buildChain(3, "c")

	got := Chain(events, nil, KeyRing{})

	if got.Outcome != OutcomeIntactUnattested {
		t.Fatalf("Outcome = %v, want OutcomeIntactUnattested", got.Outcome)
	}
	if got.AttestedThroughSeq != 0 {
		t.Fatalf("AttestedThroughSeq = %d, want 0 (nothing attested)", got.AttestedThroughSeq)
	}
	if got.UnattestedFromSeq != 1 {
		t.Fatalf("UnattestedFromSeq = %d, want 1 (the very first event)", got.UnattestedFromSeq)
	}
}

func TestChainUnattestedWhenNoEventsAtAll(t *testing.T) {
	got := Chain(nil, nil, KeyRing{})

	if got.Outcome != OutcomeIntactUnattested {
		t.Fatalf("Outcome = %v, want OutcomeIntactUnattested", got.Outcome)
	}
	if got.EventsVerified != 0 || got.AttestedThroughSeq != 0 || got.UnattestedFromSeq != 0 {
		t.Fatalf("expected all-zero facts for an empty chain, got %+v", got)
	}
}

// ---- OutcomeBroken: a tampered event fails recompute, names the first
// divergent point ----

func TestChainBrokenTamperedEventNamesFirstDivergentIndex(t *testing.T) {
	events := buildChain(5, "d")
	events[2].Target = "decision:forged" // seq 3, index 2 — field changed post-hash

	got := Chain(events, nil, KeyRing{})

	if got.Outcome != OutcomeBroken {
		t.Fatalf("Outcome = %v, want OutcomeBroken", got.Outcome)
	}
	if got.BrokenAtIndex != 2 {
		t.Fatalf("BrokenAtIndex = %d, want 2", got.BrokenAtIndex)
	}
}

// ---- OutcomeBroken: a head that verifies but doesn't match what the chain
// actually recomputes to at its seq (chain substitution) ----

func TestChainBrokenWhenHeadDisagreesWithRecomputedChainAtItsSeq(t *testing.T) {
	pub, sk := testKeypair(t)
	// Sign a head over chain A's value at seq 3.
	chainA := buildChain(4, "chainA")
	head := signHeadAt(t, sk, chainA, 3)

	// Verify against chain B — a DIFFERENT, independently-valid chain. Its
	// own linkage recomputes fine; only the head/event disagreement at seq
	// 3 exposes the substitution.
	chainB := buildChain(4, "chainB")

	got := Chain(chainB, []chain.Head{head}, NewKeyRing(pub))

	if got.Outcome != OutcomeBroken {
		t.Fatalf("Outcome = %v, want OutcomeBroken", got.Outcome)
	}
	if got.BrokenAtIndex != 2 { // seq 3 is index 2
		t.Fatalf("BrokenAtIndex = %d, want 2", got.BrokenAtIndex)
	}
}

// ---- OutcomeBroken: a bad head signature, distinct from an unknown key id ----

func TestChainBrokenOnBadHeadSignature(t *testing.T) {
	pub, sk := testKeypair(t)
	events := buildChain(4, "e")
	head := signHeadAt(t, sk, events, 4)
	head.Signature = flipHexChar(head.Signature) // corrupt, still valid hex

	got := Chain(events, []chain.Head{head}, NewKeyRing(pub))

	if got.Outcome != OutcomeBroken {
		t.Fatalf("Outcome = %v, want OutcomeBroken", got.Outcome)
	}
	if got.UnknownKeyID {
		t.Fatal("a bad signature under a KNOWN key must not be reported as UnknownKeyID")
	}
	if got.BadHeadSeq != 4 {
		t.Fatalf("BadHeadSeq = %d, want 4", got.BadHeadSeq)
	}
	if got.BrokenAtIndex != -1 {
		t.Fatalf("BrokenAtIndex = %d, want -1 (this is a head failure, not an event index)", got.BrokenAtIndex)
	}
}

func TestChainBrokenOnUnknownKeyID(t *testing.T) {
	_, skA := testKeypair(t)
	pubOther, _ := testKeypair(t)
	events := buildChain(4, "f")
	head := signHeadAt(t, skA, events, 4)

	// The ring holds a real key, but not the one this head was signed
	// under — keys are additive-forever (D5): this is a rotation/operator
	// question, never a tamper finding by itself.
	got := Chain(events, []chain.Head{head}, NewKeyRing(pubOther))

	if got.Outcome != OutcomeBroken {
		t.Fatalf("Outcome = %v, want OutcomeBroken", got.Outcome)
	}
	if !got.UnknownKeyID {
		t.Fatal("expected UnknownKeyID = true for a key id the ring doesn't recognize")
	}
	if got.BadHeadKeyID != head.KeyID {
		t.Fatalf("BadHeadKeyID = %q, want %q", got.BadHeadKeyID, head.KeyID)
	}
}

// ---- multiple keys in the ring, selected correctly per head (rotation) ----

func TestChainKeyRingSelectsCorrectKeyPerHead(t *testing.T) {
	pubA, skA := testKeypair(t)
	pubB, skB := testKeypair(t)
	events := buildChain(6, "g")

	headA := signHeadAt(t, skA, events, 3)
	headB := signHeadAt(t, skB, events, 6)
	ring := NewKeyRing(pubA, pubB)

	// Heads supplied out of order on purpose — Chain must sort them itself.
	got := Chain(events, []chain.Head{headB, headA}, ring)

	if got.Outcome != OutcomeIntactAttested {
		t.Fatalf("Outcome = %v, want OutcomeIntactAttested", got.Outcome)
	}
	if got.AttestedThroughSeq != 6 {
		t.Fatalf("AttestedThroughSeq = %d, want 6", got.AttestedThroughSeq)
	}
}

func TestNewKeyRingDerivesKeyIDsIndependently(t *testing.T) {
	pub, _ := testKeypair(t)
	ring := NewKeyRing(pub)
	if _, ok := ring[chain.KeyIDFor(pub)]; !ok {
		t.Fatal("expected the ring to be keyed by chain.KeyIDFor(pub)")
	}
}
