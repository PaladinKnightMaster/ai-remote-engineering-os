// Package verify implements ADR-0069 D6's chain-authenticity proof: the two
// steps together, in one place.
//
// # Why one package, two entry points
//
// Two implementations of the same verification rule is exactly how a
// detector comes to disagree with the writer — and that class of bug is why
// this ticket (T-126a) exists at all: internal/anchor's Classify compared
// event seqs where it should have counted heads, shipped, and passed
// review because its own test encoded a state the system's cadence design
// makes impossible. This package is the ONE place D6's proof is coded.
// audit-service's own `verify` subcommand (T-126a) and T-126b's standalone
// `aireos-verify` binary both call Chain unchanged rather than each growing
// their own copy of this logic. One implementation, two entry points,
// structurally unable to drift apart.
//
// # Streaming is the only implementation (T-126b)
//
// Chain consumes events through the EventSource interface below rather than
// a materialized []audit.Event slice. `aireos-verify` runs on the AUDITOR's
// machine against an export whose size scales with the CUSTOMER's chain, not
// ours — a slice-based Chain would inherit an O(n) memory bound on exactly
// the operation that must survive a large export, undoing the streaming
// discipline T-125c fought for on the write side. There is deliberately NO
// second `ChainStream` function: main.go's `runVerify` (DB-backed) and
// `aireos-verify` (export-file-backed) both call this same Chain, the first
// via SliceSource (below) wrapping the slice it already accumulates for
// other reasons, the second via internal/export's streaming Parser, which
// satisfies EventSource structurally with no adapter needed. Two entry
// points, one proof, never two copies of it to drift apart.
//
// # The two-step proof (D6), in order
//
//  1. Verify every head's Ed25519 signature against a supplied key ring
//     (chain.VerifyHead). This step ALONE PROVES NOTHING ABOUT THE EVENTS —
//     a valid signature over a head value says only that someone holding
//     the signing key signed those exact bytes at some point. It says
//     nothing about whether the event chain a caller is holding actually
//     recomputes to the value that was signed.
//  2. Walk the events in chain order from chain.Genesis, recomputing each
//     chainHash with chain.Compute, and confirm the recomputed chain hash
//     AT EACH HEAD'S SEQ equals that head's stored chainHash. This is what
//     actually ties a verified signature to the data it is supposed to
//     attest — without it, an attacker who substitutes an entirely
//     different but internally-consistent event stream could present a
//     genuinely-signed (but non-matching) head alongside it and step 1
//     alone would not catch the substitution.
//
// Heads are still taken as a plain slice, deliberately not streamed: step 1
// needs every head up front (D6 requires ALL head signatures verified before
// step 2 begins at all), and heads are bounded to ~1% of event volume by
// construction (ADR-0069 D2's cadence, PERF-15's anchor-file size bound) —
// loading them fully is deliberate and safe, unlike the potentially-unbounded
// event stream.
//
// # Three outcomes, never two
//
// A caller must never collapse "the chain recomputes but nothing beyond
// some point is attested" into a bare "intact" a reader could mistake for
// fully attested. Result.Outcome is always exactly one of OutcomeIntactAttested,
// OutcomeIntactUnattested, or OutcomeBroken — see their doc comments.
package verify

import (
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"

	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

// EventSource yields chain events one at a time, in ascending seq order,
// starting logically at chain.Genesis — the incremental counterpart to a
// materialized []audit.Event slice (see the package doc's "Streaming is the
// only implementation"). Next returns ok=false with a nil error when the
// source is exhausted (never an error for ordinary end-of-input). A non-nil
// error means the source itself failed to produce the next event — a
// read/parse failure distinct from anything Chain's own D6 classification
// reports — and Chain stops immediately and propagates it; callers must
// treat that case as "verification could not be completed" rather than any
// of the three Result outcomes.
type EventSource interface {
	Next() (audit.Event, bool, error)
}

// SliceSource adapts an in-memory []audit.Event to EventSource. Its Next
// never errors. This is the ONLY sanctioned way to feed Chain from a
// slice — a caller must not invent a second EventSource implementation for
// this; SliceSource exists precisely so main.go's DB-backed `runVerify`
// (which already accumulates the whole chain into a slice for reasons
// unrelated to this refactor) can keep doing that without duplicating
// iteration logic. A NEW caller should stream from its actual source
// instead of materializing a slice just to wrap it here.
type SliceSource struct {
	events []audit.Event
	i      int
}

// NewSliceSource wraps events for consumption by Chain.
func NewSliceSource(events []audit.Event) *SliceSource {
	return &SliceSource{events: events}
}

// Next implements EventSource.
func (s *SliceSource) Next() (audit.Event, bool, error) {
	if s.i >= len(s.events) {
		return audit.Event{}, false, nil
	}
	e := s.events[s.i]
	s.i++
	return e, true, nil
}

// Outcome is ADR-0069 D6's three-way verification result.
type Outcome int

const (
	// OutcomeIntactAttested: the chain recomputes end to end from Genesis
	// AND every supplied head verifies under a known key, with the last
	// event's seq covered by a verified head. AttestedThroughSeq names the
	// prefix a third party can trust purely from the signatures.
	OutcomeIntactAttested Outcome = iota
	// OutcomeIntactUnattested: the chain recomputes end to end with no
	// linkage or hash failure, but the tail is not covered by a verified
	// head — either no head exists at all, or events beyond the last
	// verified head's seq have not yet been checkpointed. Report the tail
	// explicitly (UnattestedFromSeq); never print a bare "intact".
	OutcomeIntactUnattested
	// OutcomeBroken: either the chain fails to recompute (a prevHash/
	// chainHash mismatch, or a head's chainHash disagrees with the freshly
	// recomputed value at that seq — BrokenAtIndex names the first
	// divergent event), or a head's own Ed25519 signature fails to verify
	// under a known key (BadHeadSeq/BadHeadKeyID name it, UnknownKeyID
	// distinguishes an unrecognized key id from an actual bad signature).
	OutcomeBroken
)

// Result is Chain's return value: which of the three outcomes applies, plus
// every fact needed to report it without a caller re-deriving anything.
type Result struct {
	Outcome Outcome
	// EventsVerified is len(events) — the number of events walked.
	EventsVerified int
	// AttestedThroughSeq is the highest event seq covered by a verified
	// head signature. 0 when nothing is attested at all.
	AttestedThroughSeq int64
	// UnattestedFromSeq is the seq of the first event beyond
	// AttestedThroughSeq — only set (nonzero) on OutcomeIntactUnattested
	// with at least one event present.
	UnattestedFromSeq int64
	// BrokenAtIndex is the 0-based index, in stream order, of the first
	// divergent event — only meaningful (>= 0) when Outcome ==
	// OutcomeBroken from a recompute/linkage failure (a chain break, not a
	// head signature failure). -1 otherwise.
	BrokenAtIndex int
	// BrokenEventSeq / BrokenEventID name the SAME event BrokenAtIndex
	// points at, captured directly at the moment of divergence — so a
	// streaming caller (aireos-verify) can report exactly which event
	// diverged without retaining the whole event slice just to index back
	// into it the way main.go's runVerify does with its own `all` slice.
	// Zero/empty unless BrokenAtIndex >= 0.
	BrokenEventSeq int64
	BrokenEventID  string
	// BadHeadSeq / BadHeadKeyID name the first head (in ascending seq
	// order) that failed step 1 — only meaningful when Outcome ==
	// OutcomeBroken from a head failure (BrokenAtIndex == -1 in that case).
	BadHeadSeq   int64
	BadHeadKeyID string
	// UnknownKeyID is true when the head failure above was because no
	// supplied key matches BadHeadKeyID (chain.ErrUnknownKeyID) — an
	// operator/rotation question (keys are additive-forever, ADR-0069 D5),
	// reported distinctly from an actual bad signature
	// (chain.ErrInvalidHeadSignature), which IS a tamper finding.
	UnknownKeyID bool
}

// KeyRing is a set of Ed25519 public keys, selected per head by its KeyID
// (chain.KeyIDFor's fingerprint) — ADR-0069 D5: keys are additive-forever,
// so a verifier must be able to hold more than one at once across a
// rotation, never just the single "current" key.
type KeyRing map[string]ed25519.PublicKey

// NewKeyRing builds a KeyRing from a list of public keys, deriving each
// entry's map key via chain.KeyIDFor — the same derivation VerifyHead uses,
// so a key added here is found by exactly the heads it actually signed.
func NewKeyRing(pubs ...ed25519.PublicKey) KeyRing {
	kr := make(KeyRing, len(pubs))
	for _, pub := range pubs {
		kr[chain.KeyIDFor(pub)] = pub
	}
	return kr
}

// Chain runs ADR-0069 D6's two-step proof (see the package doc for the full
// rationale, including why events is an EventSource rather than a slice)
// over events (must yield events in chain order — ascending seq, starting
// at chain.Genesis) and heads (any order; Chain sorts a copy ascending by
// seq itself), verifying each head against keys.
//
// Step 1 runs first, over every head, in ascending seq order: the FIRST
// head that fails (unknown key id, or a bad signature) makes the whole
// result OutcomeBroken immediately — step 2 does not run at all in that
// case, matching D6's "in this order" requirement (a signature failure
// makes the rest of the proof moot). events is never touched at all in
// this case — the source is never read.
//
// Step 2 then walks events once, incrementally, recomputing prevHash/
// chainHash the same way chain.Verify does, and additionally checks — at
// every seq a head exists for — that the head's stored chainHash equals the
// just-recomputed value. Either kind of failure (linkage/recompute, or a
// head/event chainHash disagreement) reports OutcomeBroken with
// BrokenAtIndex/BrokenEventSeq/BrokenEventID naming the first divergent
// event.
//
// The non-error return is always one of the three D6 outcomes; a non-nil
// error means events itself failed mid-stream (a read/parse failure) and
// the returned Result must not be treated as any of them — see EventSource's
// doc comment.
//
// UnattestedFromSeq (D6's "the unattested tail is reported, never hidden")
// is computed in the SAME single forward pass as the recompute, not a
// second pass over a re-readable slice (a stream cannot be re-read):
// whenever an event crosses a verified head, any earlier "first unattested"
// candidate is invalidated (everything up to and including that head is now
// known-attested) and the search for a new candidate resumes from there.
// Since events arrive in ascending seq order, this reproduces exactly what
// a second full pass over the finished slice would have found, in one pass.
func Chain(events EventSource, heads []chain.Head, keys KeyRing) (Result, error) {
	result := Result{BrokenAtIndex: -1}

	sortedHeads := make([]chain.Head, len(heads))
	copy(sortedHeads, heads)
	sort.Slice(sortedHeads, func(i, j int) bool { return sortedHeads[i].Seq < sortedHeads[j].Seq })

	// Step 1 (D6): every head's signature, before any recompute happens.
	for _, h := range sortedHeads {
		pub, ok := keys[h.KeyID]
		if !ok {
			result.Outcome = OutcomeBroken
			result.BadHeadSeq = h.Seq
			result.BadHeadKeyID = h.KeyID
			result.UnknownKeyID = true
			return result, nil
		}
		if err := chain.VerifyHead(pub, h); err != nil {
			result.Outcome = OutcomeBroken
			result.BadHeadSeq = h.Seq
			result.BadHeadKeyID = h.KeyID
			result.UnknownKeyID = err == chain.ErrUnknownKeyID
			return result, nil
		}
	}

	// Step 2 (D6): recompute from Genesis, checking each head's seq against
	// the freshly recomputed chain hash at that exact point.
	headBySeq := make(map[int64]chain.Head, len(sortedHeads))
	for _, h := range sortedHeads {
		headBySeq[h.Seq] = h
	}

	prev := chain.Genesis
	var lastAttestedSeq int64
	var lastSeq int64
	var unattestedCandidate int64
	idx := 0
	for {
		e, ok, err := events.Next()
		if err != nil {
			return result, fmt.Errorf("verify: read next event (after %d verified): %w", idx, err)
		}
		if !ok {
			break
		}

		if e.PrevHash != prev {
			result.Outcome = OutcomeBroken
			result.BrokenAtIndex = idx
			result.BrokenEventSeq = e.Seq
			result.BrokenEventID = e.ID
			result.EventsVerified = idx
			return result, nil
		}
		computed := chain.Compute(prev, e)
		if !strings.EqualFold(computed, e.ChainHash) {
			result.Outcome = OutcomeBroken
			result.BrokenAtIndex = idx
			result.BrokenEventSeq = e.Seq
			result.BrokenEventID = e.ID
			result.EventsVerified = idx
			return result, nil
		}
		if h, ok := headBySeq[e.Seq]; ok {
			if !strings.EqualFold(h.ChainHash, e.ChainHash) {
				// The event's own chainHash recomputes fine in isolation,
				// but disagrees with what was actually signed at this seq
				// — exactly the substitution step 1 alone cannot catch.
				result.Outcome = OutcomeBroken
				result.BrokenAtIndex = idx
				result.BrokenEventSeq = e.Seq
				result.BrokenEventID = e.ID
				result.EventsVerified = idx
				return result, nil
			}
			lastAttestedSeq = e.Seq
			unattestedCandidate = 0 // everything up to here just became attested
		} else if unattestedCandidate == 0 && e.Seq > lastAttestedSeq {
			unattestedCandidate = e.Seq
		}
		lastSeq = e.Seq
		prev = e.ChainHash
		idx++
	}

	result.EventsVerified = idx
	result.AttestedThroughSeq = lastAttestedSeq
	if idx == 0 {
		result.Outcome = OutcomeIntactUnattested
		return result, nil
	}
	if lastAttestedSeq != 0 && lastSeq == lastAttestedSeq {
		result.Outcome = OutcomeIntactAttested
		return result, nil
	}

	result.Outcome = OutcomeIntactUnattested
	result.UnattestedFromSeq = unattestedCandidate
	return result, nil
}
