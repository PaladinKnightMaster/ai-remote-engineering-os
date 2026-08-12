// Package anchor implements the out-of-Postgres truncation detector for
// signed chain heads (ADR-0069 D4, T-125b).
//
// # Why this exists
//
// audit_chain_heads (T-124a) detects MUTATION: rewriting an old event
// changes chainHash at and above it, so its signed head fails to verify.
// It cannot detect TRUNCATION: an attacker who deletes events 101..150 *and*
// their chain_heads rows leaves a database that is internally consistent —
// the chain terminates at seq 100, the head at 100 verifies, and nothing
// inside Postgres reveals the loss. An append-only file OUTSIDE Postgres is
// the truncation detector. It is not a backup, and it is never used to
// reconstruct anything — its only value is being a second, independent copy
// an attacker who only has database access cannot also silently rewrite.
//
// # Format
//
// One JSON object per line (NDJSON), written O_APPEND|O_CREATE|O_WRONLY,
// file mode 0640 (the containing directory is already 750, owned by the
// service user — provisioned by install.sh at T-125a). Two record shapes
// share the file, distinguished unambiguously by the "kind" field so a
// verifier (T-126) never has to guess which it is parsing:
//
//   - KindHead: mirrors chain.Head exactly (seq/chainHash/timestamp/keyId/
//     signature) — one per signed checkpoint.
//   - KindInit: a marker with only seq (+ a human-readable note) and no
//     signature fields — written whenever the file is (re)created, see (d)
//     below. It attests nothing on its own; it marks a boundary before
//     which the file has no history.
//
// # Binding rules (T-126's verifier must match these exactly)
//
// (a) ORDERING. The caller MUST append a head only AFTER the database
// transaction that inserted it has committed — see internal/checkpoint's
// Sweeper.anchorIfSigned, called strictly after WithCheckpointLock returns.
// This package does not and cannot enforce that itself; it trusts the
// caller. Anchoring before commit would mean every crash between the two
// writes leaves the anchor holding a head the DB never committed — which is
// exactly the TRUNCATION signature (c) below — so ordinary restarts would
// look like attacks. That false-positive cost is why the order is fixed.
//
// (b) IDEMPOTENT BY SEQ. Append checks the last HEAD record's seq before
// writing; if it already equals the seq being appended, the write is a
// silent no-op. A double-write after a crash (the checkpoint committed, the
// service died before or during the anchor write, and a later run retries
// the same head) must never make the file itself look divergent.
//
// (c) THREE-WAY DIVERGENCE CLASSIFICATION (Classify, below) — this
// supersedes any two-way mutate-vs-clean framing: comparing the anchor's
// last recorded position against the database's full set of signed heads
// has three outcomes, not two. The comparison unit is a COUNT OF HEADS, NOT
// a seq subtraction — heads are cadence-spaced (D2: one per ~100 events or
// 60s), so consecutive head seqs sit nothing like consecutive integers
// (e.g. 100, 213, 340). An earlier version of this file classified by
// subtracting event seqs, which made outcome 1 below unreachable in
// production and mislabeled the resulting seq gap "heads" in its own log
// line — Classify's Classification.HeadsBehind is the fix: a real count,
// computed once, here, and never recomputed by a caller:
//
//  1. exactly ONE db head past the anchor's last recorded seq ->
//     DivergenceBenignCrash. A checkpoint committed to Postgres and the
//     process died before (or during) the anchor append. Self-heal by
//     appending the missing head; this is completely ordinary and must
//     never be reported as tampering.
//  2. MORE THAN ONE db head past the anchor -> DivergenceWriterBroken. The
//     anchor WRITER is broken — disk full, permission lost, bad path — not
//     the chain. This is an OPERATIONAL ALARM, never a tamper finding: a
//     full disk must never masquerade as an attack, because every false
//     tamper alarm spends down the credibility a real one needs. Log
//     loudly as operational, then self-heal by appending the missing
//     heads. (Classify also reuses this value, with HeadsBehind == 0, for
//     an unrelated tips-agree-but-coverage-gapped case — see its own doc
//     comment.)
//  3. anchor's last recorded seq AHEAD of the database's by any amount ->
//     DivergenceTruncation — the truncation signature this whole package
//     exists to catch. Log loudly and persistently. NEVER delete, rewrite,
//     or truncate the anchor to "fix" this — the divergence itself is the
//     evidence, and erasing it to make the two agree again would erase the
//     only proof the truncation happened.
//
// Independently of (c), Classify also reports a COVERAGE GAP whenever the
// anchor carries a KindInit marker and the database has head(s) with seq
// below that marker's seq — heads that exist in Postgres but were never
// independently witnessed by this anchor (see (d) immediately below for why
// that gap can exist at all). See Classification's doc comment for exactly
// how the two concerns compose.
//
// (d) MISSING ANCHOR FILE -> RECREATE WITH AN INITIALIZATION MARKER, NEVER
// SILENTLY REBUILD FROM DB HEADS. The anchor's entire value is being an
// INDEPENDENT second copy. Silently reconstructing its content from the
// database — even faithfully — destroys that independence at exactly the
// moment it matters: an attacker who truncates the database *and* deletes
// the anchor would, under silent reconstruction, end up with a "rebuilt"
// anchor that agrees perfectly with the truncated chain, defeating the
// detector entirely. Writing a distinct KindInit marker instead makes the
// gap visible to a verifier rather than letting it infer completeness that
// was never actually independently witnessed.
//
// (e) PERF-15 — NO ROTATION, the bound is proven by arithmetic, not by
// rotating segments. Heads arrive at roughly 1% of event volume (the D2
// cadence: one head per 100 events or 60 seconds, whichever comes first).
// The measured worst-case NDJSON line for a KindHead record (real-length
// hex fields: a 64-hex chainHash, a 16-hex keyId, a 128-hex signature, and
// an RFC3339Nano timestamp) is under 400 bytes including its newline
// (TestPerf15SizeBound below marshals one and asserts this directly — the
// ADR's own back-of-envelope "~200 bytes" was a rough estimate, not
// measured). So: 1,000,000 audit events -> ~10,000 heads -> under 4MB.
// Rotation is deliberately NOT implemented: it would force T-126's verifier
// to enumerate and order segments, where a missing segment becomes
// indistinguishable from truncation — reintroducing, in the verifier, the
// exact ambiguity this package exists to remove.
package anchor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"aireos.local/services/audit-service/internal/chain"
)

// RecordKind distinguishes a signed chain-head record from the
// initialization marker record (d) requires. Never conflate the two — a
// verifier keys off this field alone to parse either shape unambiguously.
type RecordKind string

const (
	// KindHead is one signed checkpoint, mirroring chain.Head exactly.
	KindHead RecordKind = "head"
	// KindInit marks that the anchor file has no history before Seq — see
	// (d) above. Carries no signature fields; it attests nothing itself.
	KindInit RecordKind = "init"
)

// Record is one NDJSON line in the anchor file.
type Record struct {
	Kind RecordKind `json:"kind"`
	Seq  int64      `json:"seq"`
	// ChainHash/Timestamp/KeyID/Signature are populated for KindHead only —
	// omitted (empty string, elided by omitempty) on a KindInit marker.
	ChainHash string `json:"chainHash,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	KeyID     string `json:"keyId,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Note is a human-readable explanation, set on KindInit records only —
	// never parsed for meaning by a verifier, informational for an operator
	// reading the raw file.
	Note string `json:"note,omitempty"`
}

// Divergence is the three-way classification (c) above compares an anchor's
// last recorded seq against the database's last signed head seq.
type Divergence int

const (
	// DivergenceNone: the anchor and database agree exactly.
	DivergenceNone Divergence = iota
	// DivergenceBenignCrash: anchor is behind by exactly 1 — an ordinary
	// crash between the DB commit and the anchor append. Self-heal.
	DivergenceBenignCrash
	// DivergenceWriterBroken: anchor is behind by more than 1 — the anchor
	// WRITER is broken (disk, permissions, path), not the chain. An
	// operational alarm, never a tamper finding. Self-heal.
	DivergenceWriterBroken
	// DivergenceTruncation: anchor is AHEAD of the database by any amount —
	// the truncation signature. Never self-heal; never touch the anchor.
	DivergenceTruncation
)

// Classification packages the (c) three-way divergence classification with
// every fact a caller needs, so no caller ever has to redo the arithmetic
// itself. That arithmetic, done wrong exactly once — comparing EVENT seqs
// instead of counting HEADS, in a shipped version of this file — made
// DivergenceBenignCrash unreachable in practice (heads are cadence-spaced,
// D2: one per ~100 events or 60s, so consecutive head seqs are nothing like
// consecutive integers) and printed a seq subtraction mislabeled "heads" on
// every ordinary restart. This type exists so that bug class cannot recur:
// every caller reads HeadsBehind, never subtracts seqs itself.
type Classification struct {
	// Divergence is the (c) three-way classification (plus the WriterBroken
	// reuse documented on Classify below for the tips-agree-but-gapped case).
	Divergence Divergence
	// HeadsBehind is the COUNT of DB heads with seq greater than the
	// anchor's last recorded seq — a count of heads, never a seq
	// subtraction. 0 whenever the anchor's last seq is >= every DB head's
	// seq (DivergenceNone, DivergenceTruncation, or the gap-only case).
	HeadsBehind int
	// CoverageStartSeq is the anchor's FIRST record's Seq when that record
	// is a KindInit marker — the earliest seq this anchor independently
	// witnesses. 0 when the anchor carries no marker at all (it claims
	// coverage back to the very start of the chain) — indistinguishable
	// from, and equally safe as, a marker legitimately written at seq 0.
	CoverageStartSeq int64
	// UnwitnessedHeadSeqs lists every DB head seq strictly less than
	// CoverageStartSeq: present in the database but never independently
	// witnessed by this anchor (ADR-0069 D4(d)'s exact reason a KindInit
	// marker is written instead of silently rebuilding). Always empty when
	// CoverageStartSeq is 0.
	UnwitnessedHeadSeqs []int64
}

// Classify compares an anchor's full recorded history (anchorRecords, file
// order — Writer.All) against the database's full set of signed chain-head
// seqs (dbHeadSeqs, any order — Store.HeadsSince(ctx, 0), which returns
// every head since every seq is > 0) and returns the (c) three-way
// divergence classification packaged with the head-count and coverage
// facts above. Pure and side-effect-free: no filesystem, no database — both
// callers (the boot-path re-sync in main.go, and T-126b's standalone
// aireos-verify binary) gather these two slices themselves and hand them
// here unchanged, so there is exactly ONE place this arithmetic is ever
// done (see the package doc's binding rule (c)).
//
// Handles every degenerate input explicitly rather than requiring a
// non-empty precondition: no anchor records at all, an anchor holding only
// a KindInit marker with no heads, and no DB heads at all all produce a
// well-defined Classification, never a panic.
//
// A coverage gap (len(UnwitnessedHeadSeqs) > 0) is independent of
// Divergence and can co-occur with any of the four values EXCEPT
// DivergenceNone, which requires both tips to agree AND no coverage gap.
// When the tips agree but a gap exists anyway (the anchor was legitimately
// recreated after DB history its new marker does not reach back to — (d)),
// there is no dedicated fifth enum value (the existing four are kept, on
// purpose) so this specific case is reported as DivergenceWriterBroken:
// like an actually-broken writer, it is an operational condition an
// operator should look at, never by itself a tamper finding — but
// HeadsBehind is 0 here (nothing is missing to self-heal), so a caller
// building a "behind by N heads" message MUST check HeadsBehind > 0 before
// printing a head count, and should report the coverage gap (via
// UnwitnessedHeadSeqs) as its own, separate condition regardless of which
// Divergence value came back.
func Classify(anchorRecords []Record, dbHeadSeqs []int64) Classification {
	var anchorLastSeq int64
	if len(anchorRecords) > 0 {
		anchorLastSeq = anchorRecords[len(anchorRecords)-1].Seq
	}

	var coverageStart int64
	if len(anchorRecords) > 0 && anchorRecords[0].Kind == KindInit {
		coverageStart = anchorRecords[0].Seq
	}

	var dbLastSeq int64
	headsBehind := 0
	var unwitnessed []int64
	for _, seq := range dbHeadSeqs {
		if seq > dbLastSeq {
			dbLastSeq = seq
		}
		if seq > anchorLastSeq {
			headsBehind++
		}
		if coverageStart > 0 && seq < coverageStart {
			unwitnessed = append(unwitnessed, seq)
		}
	}

	c := Classification{
		HeadsBehind:         headsBehind,
		CoverageStartSeq:    coverageStart,
		UnwitnessedHeadSeqs: unwitnessed,
	}
	switch {
	case anchorLastSeq == dbLastSeq && len(unwitnessed) == 0:
		c.Divergence = DivergenceNone
	case headsBehind == 1:
		c.Divergence = DivergenceBenignCrash
	case headsBehind > 1:
		c.Divergence = DivergenceWriterBroken
	case anchorLastSeq > dbLastSeq:
		c.Divergence = DivergenceTruncation
	default:
		// headsBehind == 0 and anchorLastSeq == dbLastSeq, but a coverage
		// gap exists (len(unwitnessed) > 0) — see the doc comment above.
		c.Divergence = DivergenceWriterBroken
	}
	return c
}

// Writer appends signed chain heads to a single NDJSON file outside
// Postgres (ADR-0069 D4). Safe for concurrent use: internally serialized by
// mu, though in practice the only caller (the checkpoint sweep) is already
// single-writer across the whole cluster via WithCheckpointLock's advisory
// lock (T-124a) — this package's own lock only protects against this ONE
// process's Sweeper.Checkpoint (operator `checkpoint` subcommand) and
// Sweeper.Run (the cadence goroutine) racing each other within the same
// process.
type Writer struct {
	path string
	mu   sync.Mutex
}

// New returns a Writer for the anchor file at path. Nothing is created or
// touched until the first call to Append or WriteInitMarker.
func New(path string) *Writer {
	return &Writer{path: path}
}

// Path returns the anchor file path this Writer was constructed with.
func (w *Writer) Path() string { return w.path }

// Append writes h to the anchor file, honoring the whole contract above:
// idempotent by seq (b), and if the file does not exist at all, it is first
// recreated with an initialization marker (d) before h itself is written.
//
// The caller is solely responsible for ordering (a) — this function must
// only ever be called with a head whose insertion transaction has already
// committed to Postgres. See internal/checkpoint/checkpoint.go's
// anchorIfSigned for the call site that guarantees this.
func (w *Writer) Append(h chain.Head) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	existed, err := w.existsLocked()
	if err != nil {
		return fmt.Errorf("anchor: check %s exists: %w", w.path, err)
	}

	if !existed {
		if err := w.writeRecordLocked(Record{
			Kind: KindInit,
			Seq:  h.Seq,
			Note: "anchor file was absent; recreated here with no reconstruction from Postgres — ADR-0069 D4(d)",
		}); err != nil {
			return fmt.Errorf("anchor: write init marker before seq %d: %w", h.Seq, err)
		}
	} else {
		last, ok, err := w.lastLocked()
		if err != nil {
			return fmt.Errorf("anchor: read last record before append: %w", err)
		}
		if ok && last.Kind == KindHead && last.Seq == h.Seq {
			return nil // idempotent skip (b)
		}
	}

	if err := w.writeRecordLocked(Record{
		Kind:      KindHead,
		Seq:       h.Seq,
		ChainHash: h.ChainHash,
		Timestamp: h.Timestamp,
		KeyID:     h.KeyID,
		Signature: h.Signature,
	}); err != nil {
		return fmt.Errorf("anchor: append head at seq %d: %w", h.Seq, err)
	}
	return nil
}

// WriteInitMarker explicitly writes a KindInit marker at seq (no history
// before it), creating the file if it does not exist. This is the primitive
// the startup re-sync (main.go) uses when it finds the anchor file
// completely absent at boot, with no head yet available to append right
// after it (Append's own internal marker-then-head sequencing, above,
// handles the equivalent situation mid-run when a head IS available).
func (w *Writer) WriteInitMarker(seq int64, note string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.writeRecordLocked(Record{Kind: KindInit, Seq: seq, Note: note}); err != nil {
		return fmt.Errorf("anchor: write init marker at seq %d: %w", seq, err)
	}
	return nil
}

// Last returns the last record in the file, of either kind. ok=false when
// the file does not exist or is empty — a fresh install with no heads
// signed yet, or a freshly recreated file with no marker written yet.
func (w *Writer) Last() (Record, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastLocked()
}

// All returns every record in the file, in file order (oldest first). Used
// by the startup re-sync purely for comparison against the database — NEVER
// to reconstruct or repair the file's own content (d).
func (w *Writer) All() ([]Record, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.allLocked()
}

func (w *Writer) existsLocked() (bool, error) {
	_, err := os.Stat(w.path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (w *Writer) allLocked() ([]Record, error) {
	f, err := os.Open(w.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("anchor: open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }() // read-only handle; nothing to flush

	var out []Record
	sc := bufio.NewScanner(f)
	// PERF-15 bounds this file to low single-digit MB (see the package
	// comment's arithmetic); a generous 4MB per-line ceiling is still far
	// smaller than "unbounded" and catches a genuinely corrupt file loudly
	// (an error) rather than silently truncating a line.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("anchor: parse %s: %w", w.path, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("anchor: scan %s: %w", w.path, err)
	}
	return out, nil
}

func (w *Writer) lastLocked() (Record, bool, error) {
	records, err := w.allLocked()
	if err != nil {
		return Record{}, false, err
	}
	if len(records) == 0 {
		return Record{}, false, nil
	}
	return records[len(records)-1], true, nil
}

func (w *Writer) writeRecordLocked(r Record) (err error) {
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", w.path, err)
	}
	// A write handle's Close error matters (it can surface a deferred flush
	// failure, e.g. disk full) — captured into the named return rather than
	// silently discarded, but only when Write itself did not already fail.
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", w.path, cerr)
		}
	}()

	b, mErr := json.Marshal(r)
	if mErr != nil {
		return fmt.Errorf("marshal record: %w", mErr)
	}
	b = append(b, '\n')
	if _, wErr := f.Write(b); wErr != nil {
		return fmt.Errorf("write %s: %w", w.path, wErr)
	}
	return nil
}
