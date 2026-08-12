package export

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/store"
)

const fixedTS = "2026-08-12T00:00:00.000000000Z"

// fakeStore is an in-memory stand-in for *store.Store, real enough to
// exercise Export's paging loops (Asc-only keyset by seq) while recording
// every query it receives — the mechanism the D8 test below asserts against.
type fakeStore struct {
	events []audit.Event // must be pre-sorted ascending by Seq
	heads  []chain.Head  // must be pre-sorted ascending by Seq

	mu      sync.Mutex
	queries []store.ListQuery
}

func (f *fakeStore) List(_ context.Context, q store.ListQuery) ([]audit.Event, int64, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()

	var out []audit.Event
	for _, e := range f.events {
		if e.Seq > q.Cursor {
			out = append(out, e)
			if len(out) == q.Limit {
				break
			}
		}
	}
	var last int64
	if len(out) > 0 {
		last = out[len(out)-1].Seq
	}
	return out, last, nil
}

func (f *fakeStore) HeadsSince(_ context.Context, since int64) ([]chain.Head, error) {
	var out []chain.Head
	for _, h := range f.heads {
		if h.Seq > since {
			out = append(out, h)
		}
	}
	return out, nil
}

// ChainHead mirrors store.Store.ChainHead: MAX(seq) over the fake's current
// events, 0 when empty. Reads f.events at CALL TIME — tests that mutate
// f.events between the Export-internal ChainHead call and later List calls
// (see injectingFakeStore below) rely on this being read once, up front.
func (f *fakeStore) ChainHead(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var max int64
	for _, e := range f.events {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max, nil
}

func (f *fakeStore) queriesSnapshot() []store.ListQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.ListQuery, len(f.queries))
	copy(out, f.queries)
	return out
}

type fakeAnchor struct {
	records []anchor.Record
	err     error
}

func (f *fakeAnchor) All() ([]anchor.Record, error) { return f.records, f.err }

// makeEvents builds count events with contiguous seq starting at startSeq
// and a genuinely valid chain.Compute-linked prevHash/chainHash — not
// load-bearing for most tests here, but cheap and keeps every fixture
// realistic rather than needing a separate "valid chain" variant later.
func makeEvents(startSeq, count int64) []audit.Event {
	var out []audit.Event
	prev := chain.Genesis
	for seq := startSeq; seq < startSeq+count; seq++ {
		e := audit.Event{
			Seq:            seq,
			ID:             fmt.Sprintf("evt-%d", seq),
			Timestamp:      fixedTS,
			Actor:          "operator:test",
			Edge:           "human-agent",
			Target:         "target",
			Outcome:        "allow",
			IdempotencyKey: fmt.Sprintf("idem-%d", seq),
			PrevHash:       prev,
		}
		e.ChainHash = chain.Compute(prev, e)
		out = append(out, e)
		prev = e.ChainHash
	}
	return out
}

func readLines(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var lines [][]byte
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func decodeManifest(t *testing.T, data []byte) ManifestRecord {
	t.Helper()
	lines := readLines(t, data)
	if len(lines) == 0 {
		t.Fatal("export produced no output")
	}
	var m ManifestRecord
	if err := json.Unmarshal(lines[0], &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if m.Kind != KindManifest {
		t.Fatalf("first line kind = %q, want %q", m.Kind, KindManifest)
	}
	return m
}

func mustUnmarshalKind(t *testing.T, line []byte, wantKind string, v any) {
	t.Helper()
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		t.Fatalf("unmarshal kind probe: %v (line: %s)", err, line)
	}
	if probe.Kind != wantKind {
		t.Fatalf("kind = %q, want %q (line: %s)", probe.Kind, wantKind, line)
	}
	if err := json.Unmarshal(line, v); err != nil {
		t.Fatalf("unmarshal %s record: %v", wantKind, err)
	}
}

// TestManifestIsFirstLineWithVersion is the basic shape test: line 1 decodes
// as a manifest and carries the current FormatVersion.
func TestManifestIsFirstLineWithVersion(t *testing.T) {
	fs := &fakeStore{events: makeEvents(1, 2)}
	ex := &Exporter{Store: fs}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	m := decodeManifest(t, buf.Bytes())
	if m.FormatVersion != FormatVersion {
		t.Fatalf("manifest formatVersion = %d, want %d", m.FormatVersion, FormatVersion)
	}
}

// TestAllKindsRoundTrip proves the ordering contract (manifest, events
// ascending, heads ascending, anchor records in file order) and that every
// kind decodes cleanly into its typed record.
func TestAllKindsRoundTrip(t *testing.T) {
	events := makeEvents(1, 3)
	heads := []chain.Head{
		{Seq: 2, ChainHash: "h2", Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig2"},
		{Seq: 3, ChainHash: "h3", Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig3"},
	}
	anchors := []anchor.Record{
		{Kind: anchor.KindInit, Seq: 0, Note: "no history before this point"},
		{Kind: anchor.KindHead, Seq: 2, ChainHash: "h2", Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig2"},
	}

	fs := &fakeStore{events: events, heads: heads}
	ex := &Exporter{Store: fs, Anchor: &fakeAnchor{records: anchors}}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	lines := readLines(t, buf.Bytes())
	wantLen := 1 + len(events) + len(heads) + len(anchors)
	if len(lines) != wantLen {
		t.Fatalf("got %d lines, want %d", len(lines), wantLen)
	}

	var m ManifestRecord
	mustUnmarshalKind(t, lines[0], KindManifest, &m)

	idx := 1
	for i, e := range events {
		var er EventRecord
		mustUnmarshalKind(t, lines[idx], KindEvent, &er)
		if er.Seq != e.Seq {
			t.Fatalf("event %d: seq = %d, want %d", i, er.Seq, e.Seq)
		}
		idx++
	}
	for i, h := range heads {
		var hr HeadRecord
		mustUnmarshalKind(t, lines[idx], KindHead, &hr)
		if hr.Seq != h.Seq {
			t.Fatalf("head %d: seq = %d, want %d", i, hr.Seq, h.Seq)
		}
		idx++
	}
	for i, a := range anchors {
		var ar AnchorRecord
		mustUnmarshalKind(t, lines[idx], KindAnchor, &ar)
		if ar.Seq != a.Seq {
			t.Fatalf("anchor %d: seq = %d, want %d", i, ar.Seq, a.Seq)
		}
		if ar.AnchorKind != string(a.Kind) {
			t.Fatalf("anchor %d: anchorKind = %q, want %q", i, ar.AnchorKind, a.Kind)
		}
		idx++
	}
}

// TestExportBypassesFiltersAndPresets is the ADR-0069 D8 assertion: it
// checks the QUERY every List call received, not merely the output. A
// filtered export has gaps, and gaps break prevHash linkage — this test
// exists specifically to catch a future change that adds a filter parameter
// to Exporter or otherwise narrows the query.
func TestExportBypassesFiltersAndPresets(t *testing.T) {
	fs := &fakeStore{
		events: makeEvents(1, 9), // multiple pages at PageSize=2 below
		heads:  []chain.Head{{Seq: 9, ChainHash: "h9", Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig9"}},
	}
	ex := &Exporter{Store: fs, PageSize: 2}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	queries := fs.queriesSnapshot()
	if len(queries) == 0 {
		t.Fatal("expected List to have been called at least once")
	}
	for i, q := range queries {
		if q.Direction != store.Asc {
			t.Fatalf("query %d: Direction = %v, want Asc (D8)", i, q.Direction)
		}
		if q.Actor != nil {
			t.Fatalf("query %d: Actor filter set to %q — must be nil (D8)", i, *q.Actor)
		}
		if q.Outcome != nil {
			t.Fatalf("query %d: Outcome filter set to %q — must be nil (D8)", i, *q.Outcome)
		}
		if q.Edge != nil {
			t.Fatalf("query %d: Edge filter set to %q — must be nil (D8)", i, *q.Edge)
		}
		if q.Preset {
			t.Fatalf("query %d: Preset = true — must be false (D8, ADR-0067 EVD-8)", i)
		}
	}
}

// eventCountingWriter counts how many "event" NDJSON lines have been
// flushed to the underlying writer so far. writeLine always issues exactly
// one Write call per record, so counting Write invocations that contain the
// event discriminator is an exact, non-flaky proxy for "how many event
// records have been durably written" at any point in time.
type eventCountingWriter struct {
	mu    sync.Mutex
	w     io.Writer
	lines int64
}

func (cw *eventCountingWriter) Write(p []byte) (int, error) {
	cw.mu.Lock()
	if bytes.Contains(p, []byte(`"kind":"event"`)) {
		cw.lines++
	}
	cw.mu.Unlock()
	return cw.w.Write(p)
}

func (cw *eventCountingWriter) count() int64 {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.lines
}

func pageCount(n, page int) int {
	if n == 0 {
		return 1 // List is still called once, returns an empty batch
	}
	c := n / page
	if n%page != 0 {
		c++
	}
	return c
}

// perfFakeStore wraps fakeStore and asserts, on every List call made during
// the SECOND (streaming) pass, that every event from every PRIOR page of
// that pass has already been written to the destination writer. This is the
// PERF-16 bounded-memory proof: if Export ever accumulated more than one
// page before writing, this invariant would be violated and the test would
// fail with the exact call at which it happened.
type perfFakeStore struct {
	*fakeStore
	t          *testing.T
	page       int
	passACalls int
	cw         *eventCountingWriter
	calls      int
}

func (f *perfFakeStore) List(ctx context.Context, q store.ListQuery) ([]audit.Event, int64, error) {
	f.calls++
	if f.calls > f.passACalls {
		streamCallIdx := f.calls - f.passACalls
		wantWritten := int64(streamCallIdx-1) * int64(f.page)
		if got := f.cw.count(); got != wantWritten {
			f.t.Fatalf("PERF-16 violation: before stream-pass List call #%d (cursor=%d), expected %d event lines already flushed, got %d — a page was held in memory instead of being streamed immediately",
				streamCallIdx, q.Cursor, wantWritten, got)
		}
	}
	return f.fakeStore.List(ctx, q)
}

func TestStreamEventsNeverExceedsOnePage(t *testing.T) {
	const page = 3
	events := makeEvents(1, 8) // 8 events -> pages of 3,3,2
	base := &fakeStore{events: events}

	var buf bytes.Buffer
	cw := &eventCountingWriter{w: &buf}
	pf := &perfFakeStore{fakeStore: base, t: t, page: page, passACalls: pageCount(len(events), page), cw: cw}

	ex := &Exporter{Store: pf, PageSize: page}
	if err := ex.Export(context.Background(), cw); err != nil {
		t.Fatalf("export: %v", err)
	}
	if got := cw.count(); got != int64(len(events)) {
		t.Fatalf("expected %d event lines written in total, got %d", len(events), got)
	}
}

// TestUnattestedTailAccounting proves D6: events beyond the last signed
// head's seq are still exported (never dropped), and the manifest correctly
// reports both the attested prefix and the unattested tail count.
func TestUnattestedTailAccounting(t *testing.T) {
	events := makeEvents(1, 10)
	heads := []chain.Head{{Seq: 7, ChainHash: "h7", Timestamp: fixedTS, KeyID: "abc0123456789def", Signature: "sig7"}}
	fs := &fakeStore{events: events, heads: heads}
	ex := &Exporter{Store: fs, PageSize: 4}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	m := decodeManifest(t, buf.Bytes())
	if m.EventCount != 10 {
		t.Fatalf("eventCount = %d, want 10", m.EventCount)
	}
	if m.AttestedThroughSeq != 7 {
		t.Fatalf("attestedThroughSeq = %d, want 7", m.AttestedThroughSeq)
	}
	if m.UnattestedEventCount != 3 {
		t.Fatalf("unattestedEventCount = %d, want 3 (seq 8,9,10)", m.UnattestedEventCount)
	}

	lines := readLines(t, buf.Bytes())
	var eventLines int
	for _, l := range lines[1:] {
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(l, &probe); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if probe.Kind == KindEvent {
			eventLines++
		}
	}
	if eventLines != 10 {
		t.Fatalf("expected all 10 events exported despite the unattested tail, got %d event lines", eventLines)
	}
}

// TestNoHeadsMeansEverythingUnattested covers the fresh-install case: zero
// heads exist yet, so AttestedThroughSeq is 0 and every event is unattested
// — a real, expected state, not an error.
func TestNoHeadsMeansEverythingUnattested(t *testing.T) {
	fs := &fakeStore{events: makeEvents(1, 4)}
	ex := &Exporter{Store: fs}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	m := decodeManifest(t, buf.Bytes())
	if m.AttestedThroughSeq != 0 {
		t.Fatalf("attestedThroughSeq = %d, want 0", m.AttestedThroughSeq)
	}
	if m.UnattestedEventCount != 4 {
		t.Fatalf("unattestedEventCount = %d, want 4", m.UnattestedEventCount)
	}
	if m.HeadCount != 0 {
		t.Fatalf("headCount = %d, want 0", m.HeadCount)
	}
	if m.ChainHeadKeyID != "" {
		t.Fatalf("chainHeadKeyId = %q, want empty when no head exists", m.ChainHeadKeyID)
	}
	if m.ExportedThroughSeq != 4 {
		t.Fatalf("exportedThroughSeq = %d, want 4", m.ExportedThroughSeq)
	}
}

// injectingFakeStore simulates the exact production hazard the Commander's
// review caught: audit-service ingests continuously, so events can be
// appended to the real table WHILE Export's two passes are running,
// strictly after the snapshot boundary (ExportedThroughSeq) was already
// captured via ChainHead. This fake starts with an initial event set and,
// on a configured List call number, appends "extra" events to its
// underlying slice — mimicking concurrent ingest — before answering that
// call. ChainHead is read by Export exactly once, before any List call, so
// it always reports the PRE-injection tip regardless of when injection
// later happens.
type injectingFakeStore struct {
	mu       sync.Mutex
	events   []audit.Event
	heads    []chain.Head
	extra    []audit.Event
	injectAt int // List call number (1-based) at which extra is appended
	injected bool
	calls    int
}

func (f *injectingFakeStore) ChainHead(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var max int64
	for _, e := range f.events {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max, nil
}

func (f *injectingFakeStore) HeadsSince(_ context.Context, since int64) ([]chain.Head, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []chain.Head
	for _, h := range f.heads {
		if h.Seq > since {
			out = append(out, h)
		}
	}
	return out, nil
}

func (f *injectingFakeStore) List(_ context.Context, q store.ListQuery) ([]audit.Event, int64, error) {
	f.mu.Lock()
	f.calls++
	if f.calls == f.injectAt && !f.injected {
		f.injected = true
		f.events = append(f.events, f.extra...) // simulate concurrent ingest
	}
	snapshot := make([]audit.Event, len(f.events))
	copy(snapshot, f.events)
	f.mu.Unlock()

	var out []audit.Event
	for _, e := range snapshot {
		if e.Seq > q.Cursor {
			out = append(out, e)
			if len(out) == q.Limit {
				break
			}
		}
	}
	var last int64
	if len(out) > 0 {
		last = out[len(out)-1].Seq
	}
	return out, last, nil
}

// TestExportedThroughSeqBoundsBothPassesUnderConcurrentIngest is the
// regression test for the Commander's review finding on a11cc2a: without a
// snapshot boundary captured once up front, events appended between the
// count pass and the stream pass would be streamed but never counted, so
// manifest.eventCount would understate the file's actual "event" line
// count — a manifest that disagrees with its own body is indistinguishable
// from tampering to a verifier. A test against a STATIC fake store cannot
// catch this class of bug (there is nothing to inject); this fake
// specifically mutates mid-export.
func TestExportedThroughSeqBoundsBothPassesUnderConcurrentIngest(t *testing.T) {
	const page = 3
	initial := makeEvents(1, 8) // seq 1..8 — 3 pages of 3,3,2 per pass
	extra := makeEvents(9, 4)   // seq 9..12 — "arrives" mid-export
	passACalls := pageCount(len(initial), page)

	fs := &injectingFakeStore{
		events: append([]audit.Event{}, initial...),
		extra:  extra,
		// Inject exactly at the first call of the STREAM pass — the precise
		// "arrived between pass 1 and pass 2" scenario the review named.
		// (Injecting mid pass-1 is equally covered: ChainHead is read
		// before List is ever called, so the boundary is fixed regardless
		// of when injection happens relative to either pass.)
		injectAt: passACalls + 1,
	}

	ex := &Exporter{Store: fs, PageSize: page}
	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	m := decodeManifest(t, buf.Bytes())
	if m.ExportedThroughSeq != 8 {
		t.Fatalf("exportedThroughSeq = %d, want 8 (the tip BEFORE the mid-export inject)", m.ExportedThroughSeq)
	}
	if m.EventCount != 8 {
		t.Fatalf("eventCount = %d, want 8 — events injected after the snapshot was captured must not be counted", m.EventCount)
	}

	var eventLines int
	var maxSeenSeq int64
	for _, l := range readLines(t, buf.Bytes())[1:] {
		var probe struct {
			Kind string `json:"kind"`
			Seq  int64  `json:"seq"`
		}
		if err := json.Unmarshal(l, &probe); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if probe.Kind != KindEvent {
			continue
		}
		eventLines++
		if probe.Seq > maxSeenSeq {
			maxSeenSeq = probe.Seq
		}
	}
	if int64(eventLines) != m.EventCount {
		t.Fatalf("manifest says eventCount=%d but the file actually contains %d \"event\" lines — a manifest that disagrees with its own body is indistinguishable from tampering", m.EventCount, eventLines)
	}
	if maxSeenSeq > m.ExportedThroughSeq {
		t.Fatalf("an event with seq %d beyond exportedThroughSeq (%d) was streamed — the snapshot boundary was not honored", maxSeenSeq, m.ExportedThroughSeq)
	}
}

// TestMissingAnchorDegradesGracefully covers both "not configured" (nil
// AnchorReader — AIREOS_AUDIT_ANCHOR_PATH unset) and "configured but the
// file is absent/empty": export must never fail, and must never imply the
// anchor agreed with anything.
func TestMissingAnchorDegradesGracefully(t *testing.T) {
	cases := []struct {
		name string
		ar   AnchorReader
	}{
		{"not configured", nil},
		{"configured but empty", &fakeAnchor{records: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeStore{events: makeEvents(1, 2)}
			ex := &Exporter{Store: fs, Anchor: tc.ar}

			var buf bytes.Buffer
			if err := ex.Export(context.Background(), &buf); err != nil {
				t.Fatalf("export must succeed even with no anchor available: %v", err)
			}
			m := decodeManifest(t, buf.Bytes())
			if m.AnchorAvailable {
				t.Fatal("expected anchorAvailable = false")
			}
			if m.AnchorRecordCount != 0 {
				t.Fatalf("expected anchorRecordCount = 0, got %d", m.AnchorRecordCount)
			}
			if m.AnchorUnavailableReason == "" {
				t.Fatal("expected a non-empty anchorUnavailableReason")
			}
			lines := readLines(t, buf.Bytes())
			for _, l := range lines {
				var probe struct {
					Kind string `json:"kind"`
				}
				if err := json.Unmarshal(l, &probe); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if probe.Kind == KindAnchor {
					t.Fatalf("no anchor line should be emitted when the anchor is unavailable, got: %s", l)
				}
			}
		})
	}
}

// TestKeyAppearsOnlyAsFingerprint proves the public key never appears as
// key material anywhere in the export — only its KeyIDFor-style fingerprint
// (the same value chain.Head.KeyID already carries).
func TestKeyAppearsOnlyAsFingerprint(t *testing.T) {
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	keyID := chain.KeyIDFor(pub)
	if len(keyID) != 16 {
		t.Fatalf("test setup: KeyIDFor produced %d chars, want 16", len(keyID))
	}

	heads := []chain.Head{{Seq: 1, ChainHash: "h1", Timestamp: fixedTS, KeyID: keyID, Signature: "deadbeef"}}
	fs := &fakeStore{events: makeEvents(1, 1), heads: heads}
	ex := &Exporter{Store: fs}

	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	m := decodeManifest(t, buf.Bytes())
	if m.ChainHeadKeyID != keyID {
		t.Fatalf("chainHeadKeyId = %q, want %q", m.ChainHeadKeyID, keyID)
	}

	out := buf.String()
	pubHex := hex.EncodeToString(pub)
	skHex := hex.EncodeToString(sk)
	if strings.Contains(out, pubHex) {
		t.Fatal("export output contains raw public key material — must carry only the fingerprint")
	}
	if strings.Contains(out, skHex) {
		t.Fatal("export output contains raw private key material — export must never hold a signing key at all (D3)")
	}
}

// TestManifestGoldenVector pins the manifest line's exact serialized shape
// (field set, order, and JSON tag names) — the same spirit as
// chain.TestSignHeadGoldenVector. Every other test in this file decodes
// with the SAME ManifestRecord type it is testing, so a change to a JSON
// tag, a field's presence, or field order would pass the rest of the suite
// while silently changing the format T-126's verifier parses.
//
// IF THIS TEST FAILS: the manifest wire shape has changed. The fix is NEVER
// to paste in a new expected value — bump FormatVersion and teach the
// verifier (T-126) both shapes, per this package's doc comment and
// ADR-0069's stated compatibility posture.
func TestManifestGoldenVector(t *testing.T) {
	m := ManifestRecord{
		Kind:                 KindManifest,
		FormatVersion:        1,
		ExportedAt:           "2026-08-12T00:00:00.000000000Z",
		ChainHeadKeyID:       "0123456789abcdef",
		ExportedThroughSeq:   3,
		EventCount:           3,
		HeadCount:            1,
		AttestedThroughSeq:   3,
		UnattestedEventCount: 0,
		AnchorAvailable:      true,
		AnchorRecordCount:    2,
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"kind":"manifest","formatVersion":1,"exportedAt":"2026-08-12T00:00:00.000000000Z","chainHeadKeyId":"0123456789abcdef","exportedThroughSeq":3,"eventCount":3,"headCount":1,"attestedThroughSeq":3,"unattestedEventCount":0,"anchorAvailable":true,"anchorRecordCount":2}`
	if string(b) != want {
		t.Fatalf("manifest wire shape changed — the fix is to bump FormatVersion and teach the verifier both, never to update this expected value:\ngot:  %s\nwant: %s", b, want)
	}
}
