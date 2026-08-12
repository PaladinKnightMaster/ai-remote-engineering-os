// Package export produces the offline evidence-export artifact (ADR-0069
// D3/D5/D6/D8, T-125c): a self-contained NDJSON file — one JSON object per
// line — that a third party can verify without a database or a network
// connection. T-126's `aireos-verify` binary is the consumer of this format
// and parses it exactly as documented here; a future format change MUST bump
// FormatVersion and teach the verifier both versions, never silently change
// the shape a given version already produced.
//
// # Format and ordering
//
// Line 1 is always a ManifestRecord (kind "manifest"). Every subsequent line
// carries one of three kinds, in this fixed order so a streaming reader never
// has to buffer out-of-order sections:
//
//  1. "event"  — every audit_events row, ascending by seq, with NO gaps (see
//     (a) below).
//  2. "head"   — every signed chain head (audit_chain_heads), ascending by
//     seq.
//  3. "anchor" — every record from the out-of-Postgres anchor file, in file
//     order, if one was configured and non-empty (see (e) below).
//
// # (a) D8 — the export bypasses every filter and preset
//
// Events are read with store.ListQuery{Direction: store.Asc, ...} and
// Actor/Outcome/Edge left nil and Preset left false — ALWAYS. This is
// exactly the query shape main.go's `verify` subcommand already uses. A
// filtered export has gaps; gaps break prevHash linkage; a gapped export is
// unverifiable by construction, because a verifier holding only the export
// cannot distinguish "this is a filtered view" from "this chain was
// tampered with" — both look identical, and the only safe behavior is to
// report BROKEN either way. This is ADR-0067's EVD-8 chain-contiguity rule
// reaching the export path. Do not add a filter parameter to Exporter, ever.
//
// # (b) PERF-16 — stream, never materialize
//
// Export pages through audit_events, writing each record to the destination
// io.Writer as it is read, and never accumulates the whole chain into a
// slice the way main.go's `verify` subcommand does (`var all []audit.Event`)
// — that accumulation is deliberately NOT copied here. Only ONE page of
// events is ever held in memory at a time; a page is written out completely
// (and becomes eligible for GC) before the next page is fetched. Export
// makes two bounded passes over the event chain — one to accumulate exact
// counts for the manifest (which the streaming-reader contract above
// requires to be line 1, before any event line can be written), and one to
// stream the actual event records — but neither pass ever retains more than
// one page's worth of events at once, so the memory bound holds in both.
// Heads and anchor records are NOT paged: both are bounded to a small
// fraction of event volume by construction (ADR-0069 D2's ~1% cadence and
// PERF-15's own size bound on the anchor file), so loading either fully is
// deliberate and safe, unlike the potentially-unbounded event chain.
//
// Both passes are ALSO bounded to a single seq captured exactly ONCE, before
// pass 1 makes its first read (Export calls Store.ChainHead exactly once, up
// front). Without this, on a live system — audit-service ingests
// continuously; every governed action chains an event in-transaction — the
// two passes share no snapshot or transaction, so any event appended between
// the end of pass 1 and the end of pass 2 would be streamed but never
// counted: the manifest would then claim a smaller eventCount than the file
// actually contains. That is not a cosmetic off-by-k. A verifier that
// cross-checks the manifest against the body cannot distinguish "the count
// is stale because ingest kept moving" from tampering — both look
// identical, and a manifest that disagrees with its own body is the single
// worst false-tamper-finding this artifact could produce, on exactly the
// axis T-125b's three-way anchor-divergence classification was designed to
// keep honest elsewhere. `ListQuery` deliberately gains NO upper-bound field
// for this (shared store code five services depend on); the ceiling is
// enforced locally, inside countEvents/streamEvents, by comparing each
// event's Seq against the captured value. ManifestRecord.ExportedThroughSeq
// publishes that ceiling, so a reader always knows exactly where the
// snapshot ends rather than assuming it captured "everything" — which, on a
// live system, was never a coherent claim to begin with.
//
// # (c) D3 — the export never signs
//
// Exporter holds no signing key, calls no Sweeper, and forces no checkpoint.
// It emits exactly the heads that already exist. Export is
// operator-triggerable; if it forced a checkpoint, an attacker who tampered
// ABOVE the last signed head could trigger an export and obtain a signature
// over their own tampered state, with no earlier head to contradict it.
// Attesting on demand is the separate, explicit `checkpoint` subcommand's
// job — deliberately never this one's.
//
// # (d) D6 — the unattested tail is reported, never hidden
//
// Events with seq greater than the last head's seq are still exported in
// full — Export never drops them — but the manifest records exactly where
// the attested prefix ends (AttestedThroughSeq) and how many trailing events
// lie beyond it (UnattestedEventCount), so a reader can tell "attested
// through seq N, plus M unattested events" without recomputing anything.
// AttestedThroughSeq is 0 and every event is counted as unattested when no
// head has been signed yet (a fresh install, or the AUDIT_DEV_INSECURE_AUTH
// dev escape) — that is a real, expected state, never an error.
//
// # (e) The anchor section fills an ADR-0069 under-specification (G2-clean)
//
// D5 lists "events + heads + signatures + keyId" as what the export must
// carry and does not mention the anchor file at all. This package includes
// an anchor section anyway, because omitting it would make the export
// worthless for the ONE attack the anchor exists to catch: truncation is
// invisible inside Postgres by construction (deleting events 101..150 AND
// their chain_heads rows leaves a database that is perfectly
// self-consistent — D4). A database-only export therefore makes offline
// truncation detection structurally impossible, defeating the entire
// purpose of the anchor T-125b built. D4 already assigns the verifier the
// job of comparing the anchor against the database; D5 simply never said
// how the anchor's content would reach that verifier. Including it here
// fills that gap rather than contradicting any decision D5 makes, so this
// is NOT a G2 divergence — see the commit message for the explicit
// disclosure.
//
// If AIREOS_AUDIT_ANCHOR_PATH is unset, or the file is absent or empty at
// export time, Export still succeeds: it emits the events and heads
// sections normally and records in the manifest that no anchor was
// available (AnchorAvailable=false, AnchorUnavailableReason set). It never
// fails the export and never implies the anchor agreed with anything.
//
// # What this format discloses
//
// Nothing beyond what ADR-0069 D8 already documents as safe: actor and
// target identifiers, edges, outcomes, and hashes. ADR-0045's content-free
// ingest and ADR-0049's hash-in-chain model mean events never carry raw
// commands, stdout, or credentials, and ADR-0059 keeps invocation commands
// off the audit plane entirely. The chain-head public key NEVER appears in
// this format — only its KeyIDFor-style fingerprint (a 16-hex-char SHA-256
// prefix of the public key), exactly as chain.Head.KeyID already carries it.
// Export never reads AIREOS_AUDIT_CHAIN_SIGN_KEY and never imports
// crypto/ed25519's signing surface at all.
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/store"
)

// FormatVersion is this export format's version, carried on every manifest
// line from the first release of this package onward (ADR-0069
// §Consequences: "carries the -v1 domain separator so a future format is
// distinguishable rather than ambiguous"). Bump this, and only this, when
// the shape of any record kind changes — never silently change a kind's
// fields under the same version number.
const FormatVersion = 1

// Record kind discriminators — every NDJSON line after the manifest carries
// exactly one of these in its "kind" field.
const (
	KindManifest = "manifest"
	KindEvent    = "event"
	KindHead     = "head"
	KindAnchor   = "anchor"
)

// defaultPageSize bounds how many events Export holds in memory at once
// (PERF-16). 500 matches the page size the live G1 test
// (checkpoint_e2e_test.go's listWholeChain) already uses for the same
// unfiltered ascending walk.
const defaultPageSize = 500

// ManifestRecord is always the export's first line. It is the only record
// kind whose fields are computed BEFORE any other line is written — see the
// package doc comment (b) for why that forces two bounded passes over the
// event chain rather than one.
type ManifestRecord struct {
	Kind          string `json:"kind"`
	FormatVersion int    `json:"formatVersion"`
	// ExportedAt is when this export run started, RFC3339Nano UTC.
	ExportedAt string `json:"exportedAt"`
	// ChainHeadKeyID is the KeyIDFor-style fingerprint of the key that
	// signed the MOST RECENT exported head — never the key itself, and
	// never derived from any signing key material this package holds
	// (it holds none). Empty when no head has been signed yet. Under key
	// rotation (ADR-0069 D5, additive-forever) earlier heads may carry a
	// different keyId than this field; each head record carries its own
	// keyId, this field is only a quick-glance summary of the newest one.
	ChainHeadKeyID string `json:"chainHeadKeyId,omitempty"`
	// ExportedThroughSeq is the audit_events seq this export is a SNAPSHOT
	// AS OF — read once via a single Store.ChainHead call before either
	// event pass starts (see the package doc comment (b)). Both
	// countEvents and streamEvents stop at this seq, so EventCount is
	// always exact relative to it even while audit-service keeps ingesting
	// concurrently. This is a DIFFERENT boundary than AttestedThroughSeq
	// below: ExportedThroughSeq says where the SNAPSHOT ends;
	// AttestedThroughSeq says how far SIGNATURES cover. On a live system
	// ExportedThroughSeq is normally >= AttestedThroughSeq (more events
	// have been appended than have been checkpointed yet) — the gap
	// between the two is exactly UnattestedEventCount's window.
	ExportedThroughSeq int64 `json:"exportedThroughSeq"`
	// EventCount is the exact number of "event" lines that follow — every
	// event with seq <= ExportedThroughSeq. An event appended AFTER
	// ExportedThroughSeq was captured is never included, by design: this
	// field describes a snapshot, not "everything that will ever exist".
	EventCount int64 `json:"eventCount"`
	// HeadCount is the exact number of "head" lines that follow.
	HeadCount int64 `json:"headCount"`
	// AttestedThroughSeq is the highest event seq covered by a signed head
	// (D6) — 0 when no head exists yet. See ExportedThroughSeq's doc
	// comment for how the two boundaries differ.
	AttestedThroughSeq int64 `json:"attestedThroughSeq"`
	// UnattestedEventCount is how many exported events have seq greater
	// than AttestedThroughSeq (D6) — never hidden, never dropped from the
	// export itself, only called out here so a reader does not have to
	// recompute it.
	UnattestedEventCount int64 `json:"unattestedEventCount"`
	// AnchorAvailable is true only when an anchor section follows with at
	// least one record (e).
	AnchorAvailable bool `json:"anchorAvailable"`
	// AnchorRecordCount is the exact number of "anchor" lines that follow;
	// 0 when AnchorAvailable is false.
	AnchorRecordCount int64 `json:"anchorRecordCount,omitempty"`
	// AnchorUnavailableReason is set, human-readable, whenever
	// AnchorAvailable is false — never silently absent (e).
	AnchorUnavailableReason string `json:"anchorUnavailableReason,omitempty"`
}

// EventRecord is one audit_events row, wire-identical to audit.Event's own
// JSON fields plus the "kind" discriminator and Seq (audit.Event's Seq is
// `json:"-"` on the API wire type; the export includes it because a reader
// needs it to name the attested prefix and to spot any gap, even though D8
// guarantees there is none).
type EventRecord struct {
	Kind             string `json:"kind"`
	Seq              int64  `json:"seq"`
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	Actor            string `json:"actor"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Outcome          string `json:"outcome,omitempty"`
	EvidencePacketID string `json:"evidencePacketId,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey"`
	PrevHash         string `json:"prevHash"`
	ChainHash        string `json:"chainHash"`
}

// HeadRecord is one signed chain head, wire-identical to chain.Head's
// fields plus the "kind" discriminator.
type HeadRecord struct {
	Kind      string `json:"kind"`
	Seq       int64  `json:"seq"`
	ChainHash string `json:"chainHash"`
	Timestamp string `json:"timestamp"`
	KeyID     string `json:"keyId"`
	Signature string `json:"signature"`
}

// AnchorRecord is one line from the out-of-Postgres anchor file (e).
// AnchorKind mirrors anchor.RecordKind ("head" or "init") — never confuse it
// with the outer "kind" discriminator, which is always "anchor" for every
// line in this section.
type AnchorRecord struct {
	Kind       string `json:"kind"`
	AnchorKind string `json:"anchorKind"`
	Seq        int64  `json:"seq"`
	ChainHash  string `json:"chainHash,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
	KeyID      string `json:"keyId,omitempty"`
	Signature  string `json:"signature,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Store is the read surface Export needs — exactly store.Store's List,
// HeadsSince, and ChainHead method signatures, narrowed to an interface so
// tests can inject a fake that records every query it receives (D8), or
// simulates concurrent ingest mid-export, without a live Postgres.
// *store.Store satisfies this with no adapter needed. ChainHead is what
// Export reads ONCE, up front, to capture ExportedThroughSeq (see its doc
// comment and package doc (b)) — it is the same MAX(seq) position read
// store.Store.ChainHead already exposed for other callers, not a new query
// shape.
type Store interface {
	List(ctx context.Context, q store.ListQuery) ([]audit.Event, int64, error)
	HeadsSince(ctx context.Context, since int64) ([]chain.Head, error)
	ChainHead(ctx context.Context) (int64, error)
}

// AnchorReader is the read surface Export needs from the out-of-Postgres
// anchor file — exactly anchor.Writer's All() method. *anchor.Writer
// satisfies this with no adapter needed. A nil AnchorReader means
// AIREOS_AUDIT_ANCHOR_PATH was not configured for this run (e).
type AnchorReader interface {
	All() ([]anchor.Record, error)
}

// Exporter produces one evidence export (D3/D5/D6/D8). The zero value is not
// usable — Store must be set; Anchor, PageSize and Now are optional.
type Exporter struct {
	Store  Store
	Anchor AnchorReader // nil disables the anchor section (e)

	// PageSize bounds how many events are held in memory at once (b).
	// 0 uses defaultPageSize.
	PageSize int
	// Now returns the export timestamp. nil uses time.Now — overridable so
	// tests (including the golden-vector test) can pin ExportedAt.
	Now func() time.Time
}

func (ex *Exporter) pageSize() int {
	if ex.PageSize > 0 {
		return ex.PageSize
	}
	return defaultPageSize
}

func (ex *Exporter) now() time.Time {
	if ex.Now != nil {
		return ex.Now()
	}
	return time.Now()
}

// Export writes the whole NDJSON artifact to w: the manifest, then every
// event ascending by seq, then every head ascending by seq, then every
// anchor record in file order. See the package doc comment for the full
// contract; this method's structure follows it exactly, section by section.
func (ex *Exporter) Export(ctx context.Context, w io.Writer) error {
	heads, err := ex.Store.HeadsSince(ctx, 0)
	if err != nil {
		return fmt.Errorf("export: list chain heads: %w", err)
	}
	var attestedThroughSeq int64
	var keyID string
	if n := len(heads); n > 0 {
		last := heads[n-1]
		attestedThroughSeq = last.Seq
		keyID = last.KeyID
	}

	anchorRecords, anchorAvailable, anchorReason, err := ex.readAnchor()
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}

	// Capture the snapshot boundary EXACTLY ONCE, before either event pass
	// makes its first read (package doc (b) / ExportedThroughSeq's doc
	// comment) — both countEvents and streamEvents stop at this seq, so the
	// manifest's counts stay exact even while audit-service keeps ingesting
	// concurrently.
	exportedThroughSeq, err := ex.Store.ChainHead(ctx)
	if err != nil {
		return fmt.Errorf("export: read chain tip: %w", err)
	}

	page := ex.pageSize()
	eventCount, unattestedCount, err := ex.countEvents(ctx, page, attestedThroughSeq, exportedThroughSeq)
	if err != nil {
		return fmt.Errorf("export: count events: %w", err)
	}

	manifest := ManifestRecord{
		Kind:                    KindManifest,
		FormatVersion:           FormatVersion,
		ExportedAt:              ex.now().UTC().Format(time.RFC3339Nano),
		ChainHeadKeyID:          keyID,
		ExportedThroughSeq:      exportedThroughSeq,
		EventCount:              eventCount,
		HeadCount:               int64(len(heads)),
		AttestedThroughSeq:      attestedThroughSeq,
		UnattestedEventCount:    unattestedCount,
		AnchorAvailable:         anchorAvailable,
		AnchorRecordCount:       int64(len(anchorRecords)),
		AnchorUnavailableReason: anchorReason,
	}
	if err := writeLine(w, manifest); err != nil {
		return fmt.Errorf("export: write manifest: %w", err)
	}

	if err := ex.streamEvents(ctx, w, page, exportedThroughSeq); err != nil {
		return fmt.Errorf("export: stream events: %w", err)
	}

	for _, h := range heads {
		if err := writeLine(w, newHeadRecord(h)); err != nil {
			return fmt.Errorf("export: write head at seq %d: %w", h.Seq, err)
		}
	}

	for _, r := range anchorRecords {
		if err := writeLine(w, newAnchorRecord(r)); err != nil {
			return fmt.Errorf("export: write anchor record at seq %d: %w", r.Seq, err)
		}
	}

	return nil
}

// readAnchor resolves the anchor section (e). Never returns an error for
// "unconfigured" or "absent/empty" — those are ordinary, expected outcomes
// recorded in the manifest instead. A non-nil error here means the anchor
// file exists but could not be read/parsed (a genuine problem, e.g.
// corruption) and Export fails loud rather than silently reporting an empty
// anchor section for a file that is actually there but broken.
func (ex *Exporter) readAnchor() (records []anchor.Record, available bool, reason string, err error) {
	if ex.Anchor == nil {
		return nil, false, "AIREOS_AUDIT_ANCHOR_PATH not configured for this export run", nil
	}
	recs, aerr := ex.Anchor.All()
	if aerr != nil {
		return nil, false, "", fmt.Errorf("read anchor: %w", aerr)
	}
	if len(recs) == 0 {
		return nil, false, "anchor file absent or empty at export time", nil
	}
	return recs, true, "", nil
}

// countEvents makes the FIRST of Export's two bounded passes (b): it walks
// the chain with the exact D8 query shape, tallying totals and never
// retaining more than one page of events at a time, so the manifest (which
// must be line 1) can report exact counts before any event line is written.
// It STOPS at maxSeq (ExportedThroughSeq, captured once by Export before
// either pass starts) — an event with Seq > maxSeq is never counted, even if
// the store returns it (i.e. even if it was appended concurrently, after
// the snapshot boundary was captured).
func (ex *Exporter) countEvents(ctx context.Context, page int, attestedThroughSeq, maxSeq int64) (total, unattested int64, err error) {
	var cursor int64
	for {
		batch, last, lerr := ex.Store.List(ctx, store.ListQuery{
			Direction: store.Asc,
			Cursor:    cursor,
			Limit:     page,
		})
		if lerr != nil {
			return 0, 0, lerr
		}
		for _, e := range batch {
			if e.Seq > maxSeq {
				return total, unattested, nil
			}
			total++
			if e.Seq > attestedThroughSeq {
				unattested++
			}
		}
		if len(batch) < page || last >= maxSeq {
			return total, unattested, nil
		}
		cursor = last
	}
}

// streamEvents makes the SECOND of Export's two bounded passes (b): the
// exact same D8 query shape as countEvents, but this time writing each
// event line as its page is read, and discarding the page immediately after
// — never accumulating the chain into a slice (unlike main.go's `verify`
// subcommand, deliberately not copied here). It STOPS at the SAME maxSeq
// countEvents used — this is what keeps EventCount exact: without this
// bound, an event appended between the end of the count pass and the end of
// this stream pass would be written here but never tallied there.
func (ex *Exporter) streamEvents(ctx context.Context, w io.Writer, page int, maxSeq int64) error {
	var cursor int64
	for {
		batch, last, err := ex.Store.List(ctx, store.ListQuery{
			Direction: store.Asc,
			Cursor:    cursor,
			Limit:     page,
		})
		if err != nil {
			return err
		}
		for _, e := range batch {
			if e.Seq > maxSeq {
				return nil
			}
			if err := writeLine(w, newEventRecord(e)); err != nil {
				return err
			}
		}
		if len(batch) < page || last >= maxSeq {
			return nil
		}
		cursor = last
	}
}

func newEventRecord(e audit.Event) EventRecord {
	return EventRecord{
		Kind:             KindEvent,
		Seq:              e.Seq,
		ID:               e.ID,
		Timestamp:        e.Timestamp,
		Actor:            e.Actor,
		Edge:             e.Edge,
		Target:           e.Target,
		Outcome:          e.Outcome,
		EvidencePacketID: e.EvidencePacketID,
		IdempotencyKey:   e.IdempotencyKey,
		PrevHash:         e.PrevHash,
		ChainHash:        e.ChainHash,
	}
}

func newHeadRecord(h chain.Head) HeadRecord {
	return HeadRecord{
		Kind:      KindHead,
		Seq:       h.Seq,
		ChainHash: h.ChainHash,
		Timestamp: h.Timestamp,
		KeyID:     h.KeyID,
		Signature: h.Signature,
	}
}

func newAnchorRecord(r anchor.Record) AnchorRecord {
	return AnchorRecord{
		Kind:       KindAnchor,
		AnchorKind: string(r.Kind),
		Seq:        r.Seq,
		ChainHash:  r.ChainHash,
		Timestamp:  r.Timestamp,
		KeyID:      r.KeyID,
		Signature:  r.Signature,
		Note:       r.Note,
	}
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
