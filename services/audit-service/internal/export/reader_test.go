package export

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

// buildFullExport produces one export exercising every record kind (event,
// head, anchor of both KindInit and KindHead), via the REAL Exporter —
// never hand-assembled — so the round-trip test below proves the writer and
// reader agree on the actual wire format, not on a test author's
// approximation of it.
func buildFullExport(t *testing.T) (data []byte, events []audit.Event, heads []chain.Head, anchors []anchor.Record) {
	t.Helper()
	events = makeEvents(1, 5)
	heads = []chain.Head{
		{Seq: 3, ChainHash: events[2].ChainHash, Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig3"},
		{Seq: 5, ChainHash: events[4].ChainHash, Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig5"},
	}
	anchors = []anchor.Record{
		{Kind: anchor.KindInit, Seq: 0, Note: "no history before this point"},
		{Kind: anchor.KindHead, Seq: 3, ChainHash: events[2].ChainHash, Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig3"},
		{Kind: anchor.KindHead, Seq: 5, ChainHash: events[4].ChainHash, Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig5"},
	}

	fs := &fakeStore{events: events, heads: heads}
	ex := &Exporter{Store: fs, Anchor: &fakeAnchor{records: anchors}}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	return buf.Bytes(), events, heads, anchors
}

// TestParserRoundTripsEveryFieldByteForByte is the co-location contract this
// file exists to prove: build an export with every record kind via the REAL
// writer, parse it back with the REAL reader, and assert every field
// survives — manifest, every event (in order), every head (in order), every
// anchor record (in order, including the AnchorKind discriminator).
func TestParserRoundTripsEveryFieldByteForByte(t *testing.T) {
	data, wantEvents, wantHeads, wantAnchors := buildFullExport(t)

	p, err := NewParser(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	m := p.Manifest()
	if m.Kind != KindManifest || m.FormatVersion != FormatVersion {
		t.Fatalf("manifest = %+v, want kind=%q formatVersion=%d", m, KindManifest, FormatVersion)
	}
	if m.EventCount != int64(len(wantEvents)) {
		t.Fatalf("manifest.EventCount = %d, want %d", m.EventCount, len(wantEvents))
	}

	var gotEvents []audit.Event
	for {
		e, ok, err := p.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		gotEvents = append(gotEvents, e)
	}
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("got %d events, want %d", len(gotEvents), len(wantEvents))
	}
	for i, e := range wantEvents {
		if gotEvents[i] != e {
			t.Fatalf("event %d round-trip mismatch:\n got:  %+v\n want: %+v", i, gotEvents[i], e)
		}
	}

	gotHeads, err := p.Heads()
	if err != nil {
		t.Fatalf("Heads: %v", err)
	}
	if len(gotHeads) != len(wantHeads) {
		t.Fatalf("got %d heads, want %d", len(gotHeads), len(wantHeads))
	}
	for i, h := range wantHeads {
		if gotHeads[i].Head() != h {
			t.Fatalf("head %d round-trip mismatch:\n got:  %+v\n want: %+v", i, gotHeads[i].Head(), h)
		}
	}

	gotAnchors, err := p.AnchorRecords()
	if err != nil {
		t.Fatalf("AnchorRecords: %v", err)
	}
	if len(gotAnchors) != len(wantAnchors) {
		t.Fatalf("got %d anchor records, want %d", len(gotAnchors), len(wantAnchors))
	}
	for i, a := range wantAnchors {
		if gotAnchors[i].ToRecord() != a {
			t.Fatalf("anchor %d round-trip mismatch:\n got:  %+v\n want: %+v", i, gotAnchors[i].ToRecord(), a)
		}
	}
}

// TestParserTwoPassMirrorsExporterTwoPass proves the exact usage pattern
// aireos-verify relies on (package doc "Two bounded passes"): a first Parser
// over the same bytes drains events + collects heads/anchors, a second,
// independent Parser over the same bytes streams events again — and both
// passes see identical data, since the underlying bytes never change.
func TestParserTwoPassMirrorsExporterTwoPass(t *testing.T) {
	data, wantEvents, wantHeads, _ := buildFullExport(t)

	p1, err := NewParser(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("pass 1 NewParser: %v", err)
	}
	heads, err := p1.Heads()
	if err != nil {
		t.Fatalf("pass 1 Heads: %v", err)
	}
	if len(heads) != len(wantHeads) {
		t.Fatalf("pass 1: got %d heads, want %d", len(heads), len(wantHeads))
	}
	if _, err := p1.AnchorRecords(); err != nil {
		t.Fatalf("pass 1 AnchorRecords: %v", err)
	}

	p2, err := NewParser(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("pass 2 NewParser: %v", err)
	}
	var streamed []audit.Event
	for {
		e, ok, err := p2.Next()
		if err != nil {
			t.Fatalf("pass 2 Next: %v", err)
		}
		if !ok {
			break
		}
		streamed = append(streamed, e)
	}
	if len(streamed) != len(wantEvents) {
		t.Fatalf("pass 2: got %d events, want %d", len(streamed), len(wantEvents))
	}
}

// ---- strictness: every rejection is a hard error, never a skip ----

func TestParserRejectsEmptyInput(t *testing.T) {
	if _, err := NewParser(bytes.NewReader(nil)); err == nil {
		t.Fatal("expected an error for empty input")
	}
}

func TestParserRejectsMissingManifest(t *testing.T) {
	// Line 1 is an event, not a manifest — "missing manifest".
	line := `{"kind":"event","seq":1,"id":"e1"}` + "\n"
	if _, err := NewParser(strings.NewReader(line)); err == nil {
		t.Fatal("expected an error when line 1 is not a manifest record")
	}
}

func TestParserRejectsManifestNotOnLineOne(t *testing.T) {
	data, _, _, _ := buildFullExport(t)
	// Prepend an event line before the real manifest, so the manifest is now
	// on line 2 — "a manifest that is not line 1".
	shifted := `{"kind":"event","seq":0,"id":"intruder"}` + "\n" + string(data)
	if _, err := NewParser(strings.NewReader(shifted)); err == nil {
		t.Fatal("expected an error when the manifest is not line 1")
	}
}

func TestParserRejectsUnrecognizedFormatVersion(t *testing.T) {
	line := `{"kind":"manifest","formatVersion":99,"eventCount":0,"headCount":0}` + "\n"
	if _, err := NewParser(strings.NewReader(line)); err == nil {
		t.Fatal("expected an error for an unrecognized formatVersion — never best-effort")
	}
}

func TestParserRejectsUnknownKind(t *testing.T) {
	manifest := `{"kind":"manifest","formatVersion":1,"eventCount":1,"headCount":0}` + "\n"
	bogus := `{"kind":"bogus","seq":1}` + "\n"
	p, err := NewParser(strings.NewReader(manifest + bogus))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if _, _, err := p.Next(); err == nil {
		t.Fatal("expected an error for an unknown record kind")
	}
}

func TestParserRejectsMalformedLine(t *testing.T) {
	manifest := `{"kind":"manifest","formatVersion":1,"eventCount":1,"headCount":0}` + "\n"
	garbage := `not json at all` + "\n"
	p, err := NewParser(strings.NewReader(manifest + garbage))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if _, _, err := p.Next(); err == nil {
		t.Fatal("expected an error for a malformed (non-JSON) line")
	}
}

// TestParserRejectsEventCountMismatch is the T-125c regression this parser
// exists to catch: the manifest claims a different eventCount than the body
// actually contains.
func TestParserRejectsEventCountMismatch(t *testing.T) {
	data, _, _, _ := buildFullExport(t) // manifest says eventCount=5
	lines := splitLines(t, data)
	// Delete the LAST event line without touching the manifest — the
	// manifest now overclaims by one.
	var withoutOneEvent [][]byte
	removed := false
	for i := len(lines) - 1; i >= 0; i-- {
		if !removed && lineKind(t, lines[i]) == KindEvent {
			removed = true
			continue
		}
		withoutOneEvent = append([][]byte{lines[i]}, withoutOneEvent...)
	}
	if !removed {
		t.Fatal("test setup: no event line found to remove")
	}
	edited := joinLines(withoutOneEvent)

	p, err := NewParser(bytes.NewReader(edited))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var gotErr error
	for {
		_, ok, err := p.Next()
		if err != nil {
			gotErr = err
			break
		}
		if !ok {
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected an eventCount mismatch error, got nil")
	}
	if !strings.Contains(gotErr.Error(), "eventCount") {
		t.Fatalf("error = %q, want it to mention eventCount", gotErr.Error())
	}
}

// TestParserRejectsHeadCountMismatch mirrors the event-count test for the
// head section ("Same for headCount" — the ticket is explicit both must be
// checked, not just one).
func TestParserRejectsHeadCountMismatch(t *testing.T) {
	data, _, _, _ := buildFullExport(t) // manifest says headCount=2
	lines := splitLines(t, data)
	var withoutOneHead [][]byte
	removed := false
	for i := len(lines) - 1; i >= 0; i-- {
		if !removed && lineKind(t, lines[i]) == KindHead {
			removed = true
			continue
		}
		withoutOneHead = append([][]byte{lines[i]}, withoutOneHead...)
	}
	if !removed {
		t.Fatal("test setup: no head line found to remove")
	}
	edited := joinLines(withoutOneHead)

	p, err := NewParser(bytes.NewReader(edited))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if _, err := p.Heads(); err == nil {
		t.Fatal("expected a headCount mismatch error, got nil")
	} else if !strings.Contains(err.Error(), "headCount") {
		t.Fatalf("error = %q, want it to mention headCount", err.Error())
	}
}

// TestParserRejectsAnchorCountMismatch extends the same strictness rule to
// the anchor section, run unconditionally regardless of AnchorAvailable
// (reader.go's AnchorRecords doc comment).
func TestParserRejectsAnchorCountMismatch(t *testing.T) {
	data, _, _, _ := buildFullExport(t) // manifest says anchorRecordCount=3
	lines := splitLines(t, data)
	var withoutOneAnchor [][]byte
	removed := false
	for i := len(lines) - 1; i >= 0; i-- {
		if !removed && lineKind(t, lines[i]) == KindAnchor {
			removed = true
			continue
		}
		withoutOneAnchor = append([][]byte{lines[i]}, withoutOneAnchor...)
	}
	if !removed {
		t.Fatal("test setup: no anchor line found to remove")
	}
	edited := joinLines(withoutOneAnchor)

	p, err := NewParser(bytes.NewReader(edited))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if _, err := p.AnchorRecords(); err == nil {
		t.Fatal("expected an anchorRecordCount mismatch error, got nil")
	} else if !strings.Contains(err.Error(), "anchorRecordCount") {
		t.Fatalf("error = %q, want it to mention anchorRecordCount", err.Error())
	}
}

func TestParserRejectsOutOfOrderRecords(t *testing.T) {
	// A head record appears among the events, before the event section's
	// declared count is reached.
	manifest := `{"kind":"manifest","formatVersion":1,"eventCount":2,"headCount":1}` + "\n"
	ev1 := `{"kind":"event","seq":1,"id":"e1","prevHash":"` + chain.Genesis + `"}` + "\n"
	headEarly := `{"kind":"head","seq":1,"chainHash":"h"}` + "\n"
	ev2 := `{"kind":"event","seq":2,"id":"e2"}` + "\n"

	p, err := NewParser(strings.NewReader(manifest + ev1 + headEarly + ev2))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	// First event parses fine.
	if _, ok, err := p.Next(); err != nil || !ok {
		t.Fatalf("first Next: ok=%v err=%v", ok, err)
	}
	// Second call sees the head line and ends the event section early —
	// eventsSeen(1) != manifest.EventCount(2), a hard error.
	if _, _, err := p.Next(); err == nil {
		t.Fatal("expected an eventCount mismatch error from the premature head line")
	}
}

// ---- test helpers local to this file ----

func splitLines(t *testing.T, data []byte) [][]byte {
	t.Helper()
	return readLines(t, data)
}

func joinLines(lines [][]byte) []byte {
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// lineKind reads just the "kind" field of a raw NDJSON line — used by the
// count-mismatch tests above to find which line to remove, without
// asserting anything about it.
func lineKind(t *testing.T, line []byte) string {
	t.Helper()
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		t.Fatalf("probe kind: %v", err)
	}
	return probe.Kind
}
