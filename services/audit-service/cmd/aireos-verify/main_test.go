package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/export"
	"aireos.local/services/audit-service/internal/store"
	"aireos.local/services/audit-service/internal/verify"
)

const fixedTS = "2026-08-12T00:00:00.000000000Z"

// ---- test-local fakes satisfying export.Store / export.AnchorReader ----

type fakeStore struct {
	events []audit.Event // pre-sorted ascending by Seq
	heads  []chain.Head  // pre-sorted ascending by Seq
}

func (f *fakeStore) List(_ context.Context, q store.ListQuery) ([]audit.Event, int64, error) {
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

func (f *fakeStore) ChainHead(_ context.Context) (int64, error) {
	var max int64
	for _, e := range f.events {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max, nil
}

type fakeAnchorReader struct {
	records []anchor.Record
}

func (f *fakeAnchorReader) All() ([]anchor.Record, error) { return f.records, nil }

// buildChainAndHead builds n valid, linked events (seq 1..n) and a head
// signed under sk at seq n.
func buildChainAndHead(t *testing.T, sk ed25519.PrivateKey, n int64) ([]audit.Event, chain.Head) {
	t.Helper()
	var events []audit.Event
	prev := chain.Genesis
	for seq := int64(1); seq <= n; seq++ {
		e := audit.Event{
			Seq: seq, ID: fmt.Sprintf("evt-%d", seq), Timestamp: fixedTS,
			Actor: "operator:test", Edge: "human-agent", Target: "target",
			Outcome: "allow", IdempotencyKey: fmt.Sprintf("idem-%d", seq), PrevHash: prev,
		}
		e.ChainHash = chain.Compute(prev, e)
		events = append(events, e)
		prev = e.ChainHash
	}
	head, err := chain.SignHead(sk, n, events[len(events)-1].ChainHash, fixedTS)
	if err != nil {
		t.Fatalf("SignHead: %v", err)
	}
	return events, head
}

// buildExport drives the REAL Exporter (never hand-assembled) to produce a
// byte-exact export, so these binary-level tests exercise the same writer
// the round-trip tests in internal/export do.
func buildExport(t *testing.T, events []audit.Event, heads []chain.Head, anchorRecords []anchor.Record) []byte {
	t.Helper()
	fs := &fakeStore{events: events, heads: heads}
	ex := &export.Exporter{Store: fs, Anchor: &fakeAnchorReader{records: anchorRecords}}
	var buf bytes.Buffer
	if err := ex.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	return buf.Bytes()
}

func headAnchorRecord(h chain.Head) anchor.Record {
	return anchor.Record{Kind: anchor.KindHead, Seq: h.Seq, ChainHash: h.ChainHash, Timestamp: h.Timestamp, KeyID: h.KeyID, Signature: h.Signature}
}

// ---- G1-style unit legs: clean / tampered / truncated-anchor ----

func TestVerifyExportHappyPathExitsZero(t *testing.T) {
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	events, head := buildChainAndHead(t, sk, 5)
	data := buildExport(t, events, []chain.Head{head}, []anchor.Record{headAnchorRecord(head)})

	path := writeTempFile(t, data)
	var out bytes.Buffer
	code, err := verifyExport(path, mustKeyRing(t, pub), &out)
	if err != nil {
		t.Fatalf("verifyExport: unexpected usage error: %v", err)
	}
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (output: %s)", code, exitOK, out.String())
	}
	if !strings.Contains(out.String(), "INTACT+ATTESTED") {
		t.Fatalf("output missing INTACT+ATTESTED:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "IN_SYNC") {
		t.Fatalf("output missing IN_SYNC:\n%s", out.String())
	}
}

// TestVerifyExportTamperedEventExitsOne is VER-16's negative leg at the unit
// level: mutate one exported event's actor field (below the signed head, so
// step 1 alone cannot mask it) and prove verifyExport reports BROKEN and
// exits 1 — never silently accepting the tampered bytes.
func TestVerifyExportTamperedEventExitsOne(t *testing.T) {
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	events, head := buildChainAndHead(t, sk, 5)
	data := buildExport(t, events, []chain.Head{head}, []anchor.Record{headAnchorRecord(head)})

	// EvidencePacketID carries `omitempty` and is empty in this fixture, so
	// it never appears on the wire — the substituted substring matches
	// exactly the bytes newEventRecord actually emits for event seq 2.
	tampered := bytes.Replace(data, []byte(`"actor":"operator:test","edge":"human-agent","target":"target","outcome":"allow","idempotencyKey":"idem-2"`),
		[]byte(`"actor":"operator:FORGED","edge":"human-agent","target":"target","outcome":"allow","idempotencyKey":"idem-2"`), 1)
	if bytes.Equal(tampered, data) {
		t.Fatal("test setup: tamper substitution did not match any line — fixture drifted from EventRecord's JSON shape")
	}

	path := writeTempFile(t, tampered)
	var out bytes.Buffer
	code, err := verifyExport(path, mustKeyRing(t, pub), &out)
	if err != nil {
		t.Fatalf("verifyExport: unexpected usage error: %v", err)
	}
	if code != exitBroken {
		t.Fatalf("exit code = %d, want %d (output: %s)", code, exitBroken, out.String())
	}
	if !strings.Contains(out.String(), "BROKEN") {
		t.Fatalf("output missing BROKEN:\n%s", out.String())
	}

	// And the SAME untampered export must still pass — VER-16: a negative
	// proof that cannot also pass on clean input is decoration.
	cleanPath := writeTempFile(t, data)
	var cleanOut bytes.Buffer
	cleanCode, err := verifyExport(cleanPath, mustKeyRing(t, pub), &cleanOut)
	if err != nil {
		t.Fatalf("verifyExport (clean): unexpected usage error: %v", err)
	}
	if cleanCode != exitOK {
		t.Fatalf("clean export exit code = %d, want %d", cleanCode, exitOK)
	}
}

// TestVerifyExportTruncatedAnchorExitsTwo simulates ADR-0069 D4's
// truncation signature: the anchor independently witnessed a head at seq 8
// that the exported (DB-backed) head section no longer carries — as if
// events/heads above seq 5 were deleted after the anchor recorded them.
// The chain itself still recomputes cleanly (there is nothing tampered
// below the retained head), which is exactly why this must be its own exit
// code distinct from BROKEN.
func TestVerifyExportTruncatedAnchorExitsTwo(t *testing.T) {
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	events, head := buildChainAndHead(t, sk, 5) // exported/DB heads: only seq 5
	ghostHead := anchor.Record{Kind: anchor.KindHead, Seq: 8, ChainHash: "deadbeef", Timestamp: fixedTS, KeyID: head.KeyID, Signature: "ghost"}
	data := buildExport(t, events, []chain.Head{head}, []anchor.Record{headAnchorRecord(head), ghostHead})

	path := writeTempFile(t, data)
	var out bytes.Buffer
	code, err := verifyExport(path, mustKeyRing(t, pub), &out)
	if err != nil {
		t.Fatalf("verifyExport: unexpected usage error: %v", err)
	}
	if code != exitTruncation {
		t.Fatalf("exit code = %d, want %d (output: %s)", code, exitTruncation, out.String())
	}
	if !strings.Contains(out.String(), "TRUNCATION_SIGNATURE") {
		t.Fatalf("output missing TRUNCATION_SIGNATURE:\n%s", out.String())
	}

	// Same negative-proof discipline: the untruncated export must pass.
	cleanData := buildExport(t, events, []chain.Head{head}, []anchor.Record{headAnchorRecord(head)})
	cleanPath := writeTempFile(t, cleanData)
	var cleanOut bytes.Buffer
	cleanCode, err := verifyExport(cleanPath, mustKeyRing(t, pub), &cleanOut)
	if err != nil {
		t.Fatalf("verifyExport (clean): unexpected usage error: %v", err)
	}
	if cleanCode != exitOK {
		t.Fatalf("clean export exit code = %d, want %d", cleanCode, exitOK)
	}
}

func TestVerifyExportUnattestedTailExitsThree(t *testing.T) {
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	events, _ := buildChainAndHead(t, sk, 5)
	// Sign only through seq 3 — seq 4/5 are unattested tail.
	shortHead, err := chain.SignHead(sk, 3, events[2].ChainHash, fixedTS)
	if err != nil {
		t.Fatalf("SignHead: %v", err)
	}
	data := buildExport(t, events, []chain.Head{shortHead}, []anchor.Record{headAnchorRecord(shortHead)})

	path := writeTempFile(t, data)
	var out bytes.Buffer
	code, err := verifyExport(path, mustKeyRing(t, pub), &out)
	if err != nil {
		t.Fatalf("verifyExport: unexpected usage error: %v", err)
	}
	if code != exitDegraded {
		t.Fatalf("exit code = %d, want %d (output: %s)", code, exitDegraded, out.String())
	}
	if !strings.Contains(out.String(), "INTACT+UNATTESTED") {
		t.Fatalf("output missing INTACT+UNATTESTED:\n%s", out.String())
	}
}

func TestVerifyExportNoAnchorSectionExitsThreeWithAbsent(t *testing.T) {
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	events, head := buildChainAndHead(t, sk, 3)
	data := buildExport(t, events, []chain.Head{head}, nil) // no anchor records at all

	path := writeTempFile(t, data)
	var out bytes.Buffer
	code, err := verifyExport(path, mustKeyRing(t, pub), &out)
	if err != nil {
		t.Fatalf("verifyExport: unexpected usage error: %v", err)
	}
	if code != exitDegraded {
		t.Fatalf("exit code = %d, want %d (output: %s)", code, exitDegraded, out.String())
	}
	if !strings.Contains(out.String(), "ABSENT") {
		t.Fatalf("output missing ABSENT:\n%s", out.String())
	}
}

// TestVerifyExportUnreadableFileExits64 covers the usage-error bucket.
func TestVerifyExportUnreadableFileExits64(t *testing.T) {
	var out bytes.Buffer
	code, err := verifyExport(filepath.Join(t.TempDir(), "does-not-exist.ndjson"), verify.KeyRing{}, &out)
	if err == nil {
		t.Fatal("expected an error for an unreadable file")
	}
	if code != exitUsageError {
		t.Fatalf("exit code = %d, want %d", code, exitUsageError)
	}
}

// ---- exit-code priority ladder (pure function, table test) ----

func TestExitCodeLadder(t *testing.T) {
	broken := verify.Result{Outcome: verify.OutcomeBroken}
	attested := verify.Result{Outcome: verify.OutcomeIntactAttested}
	unattested := verify.Result{Outcome: verify.OutcomeIntactUnattested}

	tests := []struct {
		name     string
		result   verify.Result
		anchorV  string
		wantCode int
	}{
		{"broken beats truncation", broken, "TRUNCATION_SIGNATURE", exitBroken},
		{"broken beats in-sync", broken, "IN_SYNC", exitBroken},
		{"truncation beats attested+other", attested, "TRUNCATION_SIGNATURE", exitTruncation},
		{"both clean is the only zero", attested, "IN_SYNC", exitOK},
		{"attested but writer-broken anchor is degraded", attested, "WRITER_BROKEN", exitDegraded},
		{"unattested with in-sync anchor is still degraded", unattested, "IN_SYNC", exitDegraded},
		{"unattested with absent anchor is degraded", unattested, "ABSENT", exitDegraded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := exitCode(tc.result, tc.anchorV)
			if got != tc.wantCode {
				t.Fatalf("exitCode(%+v, %s) = %d, want %d", tc.result, tc.anchorV, got, tc.wantCode)
			}
		})
	}
}

// ---- classifyLabel ----

func TestClassifyLabelMapsEveryDivergence(t *testing.T) {
	cases := []struct {
		name string
		c    anchor.Classification
		want string
	}{
		{"none, no gap", anchor.Classification{}, "IN_SYNC"},
		{"benign crash", anchor.Classification{Divergence: anchor.DivergenceBenignCrash, HeadsBehind: 1}, "BENIGN_LAG"},
		{"writer broken", anchor.Classification{Divergence: anchor.DivergenceWriterBroken, HeadsBehind: 3}, "WRITER_BROKEN"},
		{"truncation", anchor.Classification{Divergence: anchor.DivergenceTruncation}, "TRUNCATION_SIGNATURE"},
		{"none but gap (defensive)", anchor.Classification{UnwitnessedHeadSeqs: []int64{1, 2}}, "COVERAGE_GAP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLabel(tc.c); got != tc.want {
				t.Fatalf("classifyLabel(%+v) = %q, want %q", tc.c, got, tc.want)
			}
		})
	}
}

// ---- keyring parsing: --pubkey (repeatable), --keyring (comments + multi) ----

func TestLoadKeyringAcceptsMultiplePubkeyFlags(t *testing.T) {
	pubA := genPub(t)
	pubB := genPub(t)
	ring, err := loadKeyring([]string{hex.EncodeToString(pubA), hex.EncodeToString(pubB)}, "")
	if err != nil {
		t.Fatalf("loadKeyring: %v", err)
	}
	if len(ring) != 2 {
		t.Fatalf("ring has %d keys, want 2", len(ring))
	}
	if _, ok := ring[chain.KeyIDFor(pubA)]; !ok {
		t.Fatal("ring missing pubA")
	}
	if _, ok := ring[chain.KeyIDFor(pubB)]; !ok {
		t.Fatal("ring missing pubB")
	}
}

func TestLoadKeyringParsesFileWithCommentsAndBlankLines(t *testing.T) {
	pubA := genPub(t)
	pubB := genPub(t)
	contents := fmt.Sprintf(`# aireos-verify keyring
%s   # the current key

# a rotated-out-but-still-valid-forever key (ADR-0069 D5)
%s
`, hex.EncodeToString(pubA), hex.EncodeToString(pubB))
	path := writeTempFile(t, []byte(contents))

	ring, err := loadKeyring(nil, path)
	if err != nil {
		t.Fatalf("loadKeyring: %v", err)
	}
	if len(ring) != 2 {
		t.Fatalf("ring has %d keys, want 2", len(ring))
	}
}

func TestLoadKeyringRejectsBadHex(t *testing.T) {
	if _, err := loadKeyring([]string{"not-hex"}, ""); err == nil {
		t.Fatal("expected an error for invalid --pubkey hex")
	}
}

func TestLoadKeyringRejectsUnreadableFile(t *testing.T) {
	if _, err := loadKeyring(nil, filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("expected an error for a missing --keyring file")
	}
}

// A key present in the ring but matching no head, and vice versa, are both
// exercised at the Chain level already (verify_test.go's
// TestChainBrokenOnUnknownKeyID) — loadKeyring itself has no opinion on
// whether a key is ever used, only on whether it parses.
func TestLoadKeyringEmptyIsNotAUsageError(t *testing.T) {
	ring, err := loadKeyring(nil, "")
	if err != nil {
		t.Fatalf("loadKeyring with no keys at all: %v", err)
	}
	if len(ring) != 0 {
		t.Fatalf("ring has %d keys, want 0", len(ring))
	}
}

// ---- test helpers ----

// mustKeyRing wraps a single public key into a verify.KeyRing the way a
// real --pubkey flag would resolve.
func mustKeyRing(t *testing.T, pub ed25519.PublicKey) verify.KeyRing {
	t.Helper()
	return verify.NewKeyRing(pub)
}

func genPub(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return pub
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.ndjson")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
