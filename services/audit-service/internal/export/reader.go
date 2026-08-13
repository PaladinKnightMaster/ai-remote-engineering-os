// reader.go adds the NDJSON export format's PARSER, living beside the
// Exporter that WRITES it (T-126b) — deliberately not inside the
// aireos-verify binary or any other consumer. This package already owns
// every record type (ManifestRecord/EventRecord/HeadRecord/AnchorRecord)
// and the exact wire shape T-126a's package doc calls "the format T-126's
// verifier parses ... exactly as documented here" — co-locating the reader
// here means writer and reader change together, in one file's worth of
// review, or the round-trip test in reader_test.go fails. Duplicating this
// logic inside a binary is exactly the class of defect this release exists
// to close (see internal/verify's package doc: two implementations of the
// same rule is how a detector comes to disagree with the writer).
//
// # Strictness is a security property
//
// Parser is deliberately never lenient. A caller holding only an export and
// a public key cannot tell "this line was skipped because the parser didn't
// understand it" from "this line was deleted by an attacker" — both look
// identical from the outside, so the ONLY safe behavior is to hard-error on
// anything that doesn't match the documented format exactly:
//
//   - an unknown "kind", a line that isn't valid JSON, a missing manifest,
//     or a manifest that is not exactly line 1;
//   - a formatVersion this parser does not understand (best-effort parsing
//     of an unrecognized version is exactly how a future format change
//     would silently misparse under an old binary);
//   - records appearing out of the writer's fixed section order (manifest,
//     then every "event", then every "head", then every "anchor" —
//     Exporter.Export's own order, see export.go's package doc);
//   - the manifest disagreeing with the body it describes. If
//     manifest.eventCount says 637 but the file contains 640 "event"
//     lines, that is an error, not a warning — T-125c shipped exactly this
//     bug once (a manifest computed from a snapshot boundary the body then
//     silently drifted past), and a manifest that disagrees with its own
//     body is indistinguishable from tampering. The writer now guarantees
//     eventCount/headCount/anchorRecordCount match their sections exactly;
//     this parser is the thing that checks that guarantee held. A lenient
//     parser that just skips a malformed line produces a GAP, and a gap
//     breaks prevHash linkage — at which point a verifier cannot tell "the
//     parser skipped a line" from "an event was deleted".
//
// # Streaming, matching the writer's own memory discipline
//
// Parser reads one line at a time from the underlying io.Reader — it never
// buffers the whole file. Next (the event stream) satisfies
// internal/verify.EventSource structurally (same Next() (audit.Event, bool,
// error) shape) with no adapter type needed; internal/verify never imports
// this package, and this package never imports internal/verify — the
// dependency runs one way, from a CALLER (aireos-verify) that imports both.
//
// Heads() and AnchorRecords() DO materialize their sections fully, on
// purpose: ADR-0069 D2's cadence and PERF-15's own size bound already fix
// both at roughly 1% of event volume, so holding either fully is the same
// deliberate, bounded choice internal/export's own Export method makes for
// the same reason (see export.go's package doc (b)).
//
// # Two bounded passes, not one, at the call site
//
// A single Parser instance can only walk the file forward once (a
// io.Reader is not assumed seekable). Because heads are needed BEFORE step
// 2 of ADR-0069 D6's two-step proof can run, but physically appear AFTER
// the events in this format, a caller (aireos-verify) that wants full D6
// streaming makes TWO passes over the file: one Parser to drain events
// (discarding them, which is what performs the eventCount strictness
// check) and collect Heads()+AnchorRecords(), then a second, fresh Parser
// — seeked back to the start of the SAME underlying file — whose Next is
// fed directly into internal/verify.Chain as the streaming EventSource.
// This mirrors Exporter.Export's own two-bounded-passes design on the write
// side (package doc (b)): the read side is the same idiom in reverse.
package export

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

// maxLineBytes bounds a single NDJSON line the parser will accept before
// treating an over-long line as corruption rather than growing without
// limit — the same defensive posture internal/anchor's own line reader
// takes (anchor.go's allLocked), sized generously above any real event/head/
// anchor/manifest record.
const maxLineBytes = 8 * 1024 * 1024

// phase tracks which section of the fixed manifest/event/head/anchor order
// (export.go's package doc) the Parser is currently reading.
type phase int

const (
	phaseEvents phase = iota
	phaseHeads
	phaseAnchors
	phaseDone
)

// lookahead is the one line the Parser has read from the underlying stream
// but not yet handed to a caller — needed to detect a section boundary
// (e.g. "the event section just ended because this line is a head") without
// losing the line that starts the next section.
type lookahead struct {
	kind string
	raw  []byte
}

// Parser reads one NDJSON evidence export (export.go's format), enforcing
// every strictness rule in this file's package doc. The zero value is not
// usable — construct with NewParser.
type Parser struct {
	sc       *bufio.Scanner
	manifest ManifestRecord
	phase    phase

	la    *lookahead
	laEOF bool

	eventsSeen  int64
	headsSeen   int64
	anchorsSeen int64
}

// NewParser reads and validates line 1 (must be a manifest record, must
// carry a FormatVersion this package understands) and returns a Parser
// positioned to stream the event section via Next. Any failure here —
// empty input, a line-1 that isn't valid JSON, a line-1 whose kind isn't
// "manifest", or an unrecognized FormatVersion — is a hard error; there is
// no partial/best-effort manifest.
func NewParser(r io.Reader) (*Parser, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("export parse: read manifest line: %w", err)
		}
		return nil, errors.New("export parse: empty input — a manifest record is required on line 1")
	}
	raw := append([]byte(nil), sc.Bytes()...)

	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("export parse: line 1 is not valid JSON, so it cannot be the required manifest record: %w", err)
	}
	if probe.Kind != KindManifest {
		return nil, fmt.Errorf("export parse: line 1 has kind %q, want %q — the manifest must be exactly line 1", probe.Kind, KindManifest)
	}
	var m ManifestRecord
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("export parse: malformed manifest record: %w", err)
	}
	if m.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("export parse: manifest formatVersion=%d, this parser only understands version %d — never best-effort a version mismatch", m.FormatVersion, FormatVersion)
	}

	return &Parser{sc: sc, manifest: m, phase: phaseEvents}, nil
}

// Manifest returns the validated line-1 manifest record.
func (p *Parser) Manifest() ManifestRecord { return p.manifest }

// fill reads one more line into the lookahead buffer if one is not already
// pending, validating it is well-formed JSON carrying a recognized "kind"
// (including rejecting a second "manifest" record anywhere past line 1 —
// NewParser already consumed the only legitimate one). Idempotent: calling
// fill again before the lookahead is consumed is a no-op.
func (p *Parser) fill() error {
	if p.la != nil || p.laEOF {
		return nil
	}
	if !p.sc.Scan() {
		if err := p.sc.Err(); err != nil {
			return fmt.Errorf("export parse: read line: %w", err)
		}
		p.laEOF = true
		return nil
	}
	raw := append([]byte(nil), p.sc.Bytes()...)

	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("export parse: malformed line (not valid JSON): %w", err)
	}
	switch probe.Kind {
	case KindManifest:
		return fmt.Errorf("export parse: unexpected manifest record — the manifest must be exactly line 1, never repeated")
	case KindEvent, KindHead, KindAnchor:
		p.la = &lookahead{kind: probe.Kind, raw: raw}
		return nil
	default:
		return fmt.Errorf("export parse: unknown record kind %q — never skipped, always a hard error", probe.Kind)
	}
}

// Next streams the event section, one audit.Event at a time. It returns
// ok=false (with a nil error) exactly once, the moment the event section
// ends — either because the underlying stream reached EOF, or because the
// next line belongs to the head section — at which point it ALSO checks
// eventsSeen against manifest.EventCount and returns a hard error on any
// mismatch (the manifest-vs-body strictness rule, see the package doc).
// After that point Next always returns ok=false, nil (idempotent EOF) —
// callers proceed to Heads.
//
// Next's signature ((audit.Event, bool, error)) matches
// internal/verify.EventSource structurally, by design: a *Parser can be
// passed directly as the EventSource argument to verify.Chain with no
// adapter type.
func (p *Parser) Next() (audit.Event, bool, error) {
	if p.phase != phaseEvents {
		return audit.Event{}, false, nil
	}
	if err := p.fill(); err != nil {
		return audit.Event{}, false, err
	}
	if p.laEOF || p.la.kind != KindEvent {
		p.phase = phaseHeads
		if p.eventsSeen != p.manifest.EventCount {
			return audit.Event{}, false, fmt.Errorf(
				"export parse: manifest eventCount=%d but the body contains %d event record(s) — a manifest that disagrees with its own body is indistinguishable from tampering",
				p.manifest.EventCount, p.eventsSeen)
		}
		return audit.Event{}, false, nil
	}

	var er EventRecord
	if err := json.Unmarshal(p.la.raw, &er); err != nil {
		return audit.Event{}, false, fmt.Errorf("export parse: malformed event record: %w", err)
	}
	p.la = nil
	p.eventsSeen++
	return er.Event(), true, nil
}

// drainEvents consumes any remaining event lines (discarding them) purely
// to reach the head section and run the eventCount strictness check —
// used internally by Heads so a caller may call Heads directly without
// first manually draining Next itself (the metadata-only pass described in
// the package doc's "Two bounded passes" section).
func (p *Parser) drainEvents() error {
	if p.phase != phaseEvents {
		return nil
	}
	for {
		_, ok, err := p.Next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

// Heads reads and returns every head record, enforcing that they appear
// immediately after the event section, that no unrecognized kind or
// out-of-order record interrupts them, and that headsSeen matches
// manifest.HeadCount exactly. Safe to call without first draining Next
// (it drains internally); safe to call more than once (a no-op after the
// first successful call).
func (p *Parser) Heads() ([]HeadRecord, error) {
	if err := p.drainEvents(); err != nil {
		return nil, err
	}
	if p.phase != phaseHeads {
		return nil, nil
	}

	var heads []HeadRecord
	for {
		if err := p.fill(); err != nil {
			return nil, err
		}
		if p.laEOF || p.la.kind == KindAnchor {
			break
		}
		if p.la.kind != KindHead {
			return nil, fmt.Errorf("export parse: expected a head record, got kind %q — records must appear in manifest/event/head/anchor order", p.la.kind)
		}
		var hr HeadRecord
		if err := json.Unmarshal(p.la.raw, &hr); err != nil {
			return nil, fmt.Errorf("export parse: malformed head record: %w", err)
		}
		heads = append(heads, hr)
		p.headsSeen++
		p.la = nil
	}

	p.phase = phaseAnchors
	if p.headsSeen != p.manifest.HeadCount {
		return nil, fmt.Errorf(
			"export parse: manifest headCount=%d but the body contains %d head record(s) — a manifest that disagrees with its own body is indistinguishable from tampering",
			p.manifest.HeadCount, p.headsSeen)
	}
	return heads, nil
}

// AnchorRecords reads and returns every anchor record, the same way Heads
// reads heads — including running unconditionally (never gated on
// manifest.AnchorAvailable): a tampered manifest that claims
// anchorAvailable=false while the body still carries anchor lines (or vice
// versa) must be caught here too, not silently skipped because a caller
// trusted the flag instead of the body.
func (p *Parser) AnchorRecords() ([]AnchorRecord, error) {
	if _, err := p.Heads(); err != nil {
		return nil, err
	}
	if p.phase != phaseAnchors {
		return nil, nil
	}

	var anchors []AnchorRecord
	for {
		if err := p.fill(); err != nil {
			return nil, err
		}
		if p.laEOF {
			break
		}
		if p.la.kind != KindAnchor {
			return nil, fmt.Errorf("export parse: expected an anchor record, got kind %q — records must appear in manifest/event/head/anchor order", p.la.kind)
		}
		var ar AnchorRecord
		if err := json.Unmarshal(p.la.raw, &ar); err != nil {
			return nil, fmt.Errorf("export parse: malformed anchor record: %w", err)
		}
		anchors = append(anchors, ar)
		p.anchorsSeen++
		p.la = nil
	}

	p.phase = phaseDone
	if p.anchorsSeen != p.manifest.AnchorRecordCount {
		return nil, fmt.Errorf(
			"export parse: manifest anchorRecordCount=%d but the body contains %d anchor record(s) — a manifest that disagrees with its own body is indistinguishable from tampering",
			p.manifest.AnchorRecordCount, p.anchorsSeen)
	}
	return anchors, nil
}

// Event converts an EventRecord back to audit.Event — the exact reverse of
// newEventRecord, used by Next to reconstruct events a caller can feed
// straight into internal/verify.Chain.
func (e EventRecord) Event() audit.Event {
	return audit.Event{
		Seq: e.Seq, ID: e.ID, Timestamp: e.Timestamp, Actor: e.Actor, Edge: e.Edge,
		Target: e.Target, Outcome: e.Outcome, EvidencePacketID: e.EvidencePacketID,
		IdempotencyKey: e.IdempotencyKey, PrevHash: e.PrevHash, ChainHash: e.ChainHash,
	}
}

// Head converts a HeadRecord back to chain.Head — the exact reverse of
// newHeadRecord.
func (h HeadRecord) Head() chain.Head {
	return chain.Head{Seq: h.Seq, ChainHash: h.ChainHash, Timestamp: h.Timestamp, KeyID: h.KeyID, Signature: h.Signature}
}

// ToRecord converts an AnchorRecord back to anchor.Record — the exact
// reverse of newAnchorRecord.
func (a AnchorRecord) ToRecord() anchor.Record {
	return anchor.Record{
		Kind: anchor.RecordKind(a.AnchorKind), Seq: a.Seq, ChainHash: a.ChainHash,
		Timestamp: a.Timestamp, KeyID: a.KeyID, Signature: a.Signature, Note: a.Note,
	}
}
