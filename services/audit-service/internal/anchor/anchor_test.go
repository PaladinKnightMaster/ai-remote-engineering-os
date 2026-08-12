package anchor

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"aireos.local/services/audit-service/internal/chain"
)

func testHead(seq int64) chain.Head {
	return chain.Head{
		Seq:       seq,
		ChainHash: strings.Repeat("a", 64),
		Timestamp: "2026-08-12T00:00:00.123456789Z",
		KeyID:     strings.Repeat("b", 16),
		Signature: strings.Repeat("c", 128),
	}
}

// ---- append + read-back round trip ----

func TestAppendAndReadBackRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain-heads.ndjson")
	w := New(path)

	h1 := testHead(10)
	h2 := testHead(20)
	if err := w.Append(h1); err != nil {
		t.Fatalf("append h1: %v", err)
	}
	if err := w.Append(h2); err != nil {
		t.Fatalf("append h2: %v", err)
	}

	last, ok, err := w.Last()
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if !ok {
		t.Fatal("expected a last record")
	}
	if last.Kind != KindHead || last.Seq != h2.Seq || last.ChainHash != h2.ChainHash ||
		last.Timestamp != h2.Timestamp || last.KeyID != h2.KeyID || last.Signature != h2.Signature {
		t.Fatalf("last record does not match h2: %+v", last)
	}

	all, err := w.All()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	// Expect: init marker (file was absent), head h1, head h2.
	if len(all) != 3 {
		t.Fatalf("expected 3 records (init + 2 heads), got %d: %+v", len(all), all)
	}
	if all[0].Kind != KindInit {
		t.Fatalf("expected the first record to be an init marker, got %+v", all[0])
	}
	if all[1].Kind != KindHead || all[1].Seq != h1.Seq {
		t.Fatalf("expected record 1 to be head h1, got %+v", all[1])
	}
	if all[2].Kind != KindHead || all[2].Seq != h2.Seq {
		t.Fatalf("expected record 2 to be head h2, got %+v", all[2])
	}
}

// ---- idempotent double-append of the same seq ----

func TestAppendIdempotentSameSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain-heads.ndjson")
	w := New(path)
	h := testHead(5)

	for i := 0; i < 3; i++ {
		if err := w.Append(h); err != nil {
			t.Fatalf("append attempt %d: %v", i, err)
		}
	}

	all, err := w.All()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	// init marker + exactly ONE head record, regardless of 3 append calls.
	if len(all) != 2 {
		t.Fatalf("expected exactly 2 records (init + 1 head) after 3 idempotent appends, got %d: %+v", len(all), all)
	}
	if all[1].Kind != KindHead || all[1].Seq != h.Seq {
		t.Fatalf("expected the single head record to be seq %d, got %+v", h.Seq, all[1])
	}
}

// ---- the three divergence classifications (c) ----

func TestClassifyThreeWayDivergence(t *testing.T) {
	cases := []struct {
		name      string
		anchorSeq int64
		dbSeq     int64
		want      Divergence
	}{
		{"in sync", 100, 100, DivergenceNone},
		{"zero-zero in sync", 0, 0, DivergenceNone},
		{"behind by exactly 1 -> benign crash", 100, 101, DivergenceBenignCrash},
		{"behind by many -> writer broken", 100, 150, DivergenceWriterBroken},
		{"behind by 2 -> writer broken (not benign)", 100, 102, DivergenceWriterBroken},
		{"ahead by 1 -> truncation", 101, 100, DivergenceTruncation},
		{"ahead by many -> truncation", 150, 100, DivergenceTruncation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.anchorSeq, tc.dbSeq)
			if got != tc.want {
				t.Fatalf("Classify(%d, %d) = %v, want %v", tc.anchorSeq, tc.dbSeq, got, tc.want)
			}
		})
	}
}

// ---- missing-file initialization marker: written and parseable ----

func TestMissingFileWritesInitMarkerThenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain-heads.ndjson")
	w := New(path)

	// Before anything is written, Last/All must report absence cleanly, not
	// error — a fresh install has no anchor file at all yet.
	if _, ok, err := w.Last(); err != nil || ok {
		t.Fatalf("expected ok=false, err=nil before any write, got ok=%v err=%v", ok, err)
	}
	if all, err := w.All(); err != nil || len(all) != 0 {
		t.Fatalf("expected an empty, error-free All() before any write, got %v err=%v", all, err)
	}

	h := testHead(42)
	if err := w.Append(h); err != nil {
		t.Fatalf("append: %v", err)
	}

	all, err := w.All()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected init marker + 1 head, got %d records: %+v", len(all), all)
	}
	if all[0].Kind != KindInit {
		t.Fatalf("expected the first record to be KindInit, got %+v", all[0])
	}
	if all[0].Seq != h.Seq {
		t.Fatalf("expected the init marker's seq to be the resumption point %d, got %d", h.Seq, all[0].Seq)
	}
	if all[0].Note == "" {
		t.Fatal("expected the init marker to carry a human-readable note")
	}
	// The marker must carry no signature material — it attests nothing.
	if all[0].ChainHash != "" || all[0].Signature != "" || all[0].KeyID != "" {
		t.Fatalf("expected the init marker to carry no chain-head fields, got %+v", all[0])
	}
}

func TestWriteInitMarkerExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain-heads.ndjson")
	w := New(path)

	if err := w.WriteInitMarker(7, "anchor file absent at startup"); err != nil {
		t.Fatalf("write init marker: %v", err)
	}

	last, ok, err := w.Last()
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if !ok || last.Kind != KindInit || last.Seq != 7 {
		t.Fatalf("expected an init marker at seq 7, got ok=%v %+v", ok, last)
	}
}

// ---- marker vs head records distinguishable via Kind ----

func TestMarkerAndHeadRecordsDistinguishableByKind(t *testing.T) {
	initJSON, err := json.Marshal(Record{Kind: KindInit, Seq: 1, Note: "no prior history"})
	if err != nil {
		t.Fatalf("marshal init: %v", err)
	}
	headJSON, err := json.Marshal(Record{Kind: KindHead, Seq: 1, ChainHash: "x", Timestamp: "t", KeyID: "k", Signature: "s"})
	if err != nil {
		t.Fatalf("marshal head: %v", err)
	}

	var initParsed, headParsed Record
	if err := json.Unmarshal(initJSON, &initParsed); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	if err := json.Unmarshal(headJSON, &headParsed); err != nil {
		t.Fatalf("unmarshal head: %v", err)
	}

	if initParsed.Kind != KindInit || headParsed.Kind != KindHead {
		t.Fatalf("kind not preserved across round trip: init=%q head=%q", initParsed.Kind, headParsed.Kind)
	}
	if initParsed.Kind == headParsed.Kind {
		t.Fatal("init and head records must carry distinguishable kind values")
	}
	// A consumer parsing generically (unknown which line is which) can key
	// off Kind alone, with no ambiguity from shared field names.
	if initParsed.ChainHash != "" {
		t.Fatalf("expected no chainHash on a parsed init record, got %q", initParsed.ChainHash)
	}
	if headParsed.ChainHash == "" {
		t.Fatal("expected chainHash to survive round trip on a head record")
	}
}

// ---- PERF-15: the size bound, proven, not assumed ----

func TestPerf15SizeBound(t *testing.T) {
	const n = 10000
	const perRecordCeilingBytes = 400 // see the package doc comment's arithmetic
	const totalCeilingBytes = n * perRecordCeilingBytes

	var total int
	for i := int64(0); i < n; i++ {
		b, err := json.Marshal(testHead(i))
		if err != nil {
			t.Fatalf("marshal record %d: %v", i, err)
		}
		lineLen := len(b) + 1 // + newline, as actually written
		if lineLen > perRecordCeilingBytes {
			t.Fatalf("record %d serialized to %d bytes, exceeding the documented %d-byte ceiling", i, lineLen, perRecordCeilingBytes)
		}
		total += lineLen
	}
	if total > totalCeilingBytes {
		t.Fatalf("total %d bytes for %d records exceeds the %d-byte (%.1fMB) ceiling the package comment documents",
			total, n, totalCeilingBytes, float64(totalCeilingBytes)/(1024*1024))
	}
	t.Logf("%d records serialized to %d bytes total (%.2fMB) — PERF-15 bound holds", n, total, float64(total)/(1024*1024))
}

// ---- append failure surfaces an error, never panics ----

func TestAppendFailureSurfacesError(t *testing.T) {
	// Point the anchor at a directory (not a file) — OpenFile for writing
	// against a directory path fails; Append must return that error rather
	// than panicking.
	dir := t.TempDir()
	w := New(dir)

	err := w.Append(testHead(1))
	if err == nil {
		t.Fatal("expected an error when the anchor path is a directory, got nil")
	}
}

func TestWriteInitMarkerFailureSurfacesError(t *testing.T) {
	dir := t.TempDir()
	w := New(dir)

	err := w.WriteInitMarker(0, "unreachable path")
	if err == nil {
		t.Fatal("expected an error when the anchor path is a directory, got nil")
	}
}
