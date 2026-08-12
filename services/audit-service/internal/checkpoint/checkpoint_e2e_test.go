//go:build e2e

// checkpoint_e2e_test.go is the T-124b G1 live test for the ADR-0069 chain-
// head cadence sweep. It proves the ACTUALLY-RUNNING audit-service container
// in docker-compose.e2e.yml — not this test, not a mocked boundary — signed
// a real chain head with its own sweep goroutine during the live e2e run.
//
// It lives here rather than in tests/e2e (which is a SEPARATE Go module,
// aireos.local/tests/e2e) because Go's internal-package import rule forbids
// that module from importing aireos.local/services/audit-service/internal/
// chain at all — verified empirically while authoring this ticket (`go vet`
// there fails with "use of internal package ... not allowed"). Reimplementing
// Ed25519 verification a second time inside tests/e2e to route around that
// would duplicate the exact crypto logic T-126's aireos-verify is chartered
// to own, for no real gain: this test still runs as part of `task e2e` (see
// docker-compose.e2e.yml's tester command, which now also runs this package
// under -tags e2e, in the SAME container, against the SAME live Postgres,
// after the main ./tests/e2e/... suite has generated real traffic).
//
// It connects to DATABASE_URL — the SAME cluster Postgres the live `audit`
// service writes to (docker-compose.e2e.yml's x-cluster-env) — and reads
// what that other, already-running process produced. This test process
// itself never signs anything.
//
// T-125b extends it (ADR-0069 D4/AUD-37): after proving the DB-side head,
// it also reads AIREOS_AUDIT_ANCHOR_PATH — the SAME named-volume file the
// live `audit` service anchors to (docker-compose.e2e.yml's auditanchor
// volume, mounted into both `audit` and `tester`) — and proves the anchor
// holds the identical head: same seq, chainHash and signature. This is the
// PRIMARY G1 proof that the anchor writer (internal/anchor) actually ran
// inside the live process, not merely that its unit tests pass in isolation.
package checkpoint

import (
	"context"
	"crypto/ed25519"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/store"
)

// fixtureSecretKeyHex MUST match docker-compose.e2e.yml's audit: AND tester:
// AIREOS_AUDIT_CHAIN_SIGN_KEY — a throwaway `authctl genkeypair` fixture,
// never a real deployment key (T-125 mints a fresh one per real install).
// This test derives the PUBLIC half from it; it never uses it to sign.
const fixtureSecretKeyHex = "206f72f4093b3551d7819e7f0c212bda0e2c109983a2f315532e70d1e945ad80b4909c655f888fc007e84e775f75fedcdb234b3d8fb11d1e9b40620902571413"

func TestLiveChainHeadSweepSignsRealHeads(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	sk, err := chain.SecretKeyFromHex(fixtureSecretKeyHex)
	if err != nil {
		t.Fatalf("parse fixture secret key: %v", err)
	}
	pub, ok := sk.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("fixture key has no ed25519 public half")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()
	st := store.New(pool)

	// The live `audit` service runs its own sweep at
	// AIREOS_AUDIT_CHECKPOINT_EVENTS=20 / AIREOS_AUDIT_CHECKPOINT_INTERVAL=5s
	// against this SAME database (docker-compose.e2e.yml). This test runs
	// AFTER ./tests/e2e/... in the tester's command, so real audit traffic
	// has already flowed for the whole suite's duration — poll rather than
	// assume exact timing.
	var head chain.Head
	deadline := time.Now().Add(90 * time.Second)
	for {
		h, ok, err := st.LastHead(ctx)
		if err != nil {
			t.Fatalf("last head: %v", err)
		}
		if ok {
			head = h
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no audit_chain_heads row appeared within 90s — the live sweep never signed one")
		}
		time.Sleep(2 * time.Second)
	}

	// 1. The head's chain_hash matches the LIVE chain at its seq, read
	// straight off audit_events (not recomputed by this test) — proving the
	// live process's own tip read, not a value this test derived itself.
	liveHash := chainHashAtSeq(t, pool, head.Seq)
	if head.ChainHash != liveHash {
		t.Fatalf("head at seq %d has chain_hash %s, but the live audit_events row has %s", head.Seq, head.ChainHash, liveHash)
	}

	// 2. chain.VerifyHead passes against the fixture public key — the
	// signature really was produced by the fixture secret key over exactly
	// this (seq, chainHash, timestamp) triple.
	if err := chain.VerifyHead(pub, head); err != nil {
		t.Fatalf("VerifyHead failed for the live-signed head at seq %d: %v", head.Seq, err)
	}

	// 3. chain.Verify over the WHOLE live chain still returns intact —
	// signing is off the write path and must not have perturbed the chain it
	// attests to (PERF-14 / D2).
	events := listWholeChain(t, ctx, st)
	if idx := chain.Verify(events); idx != -1 {
		t.Fatalf("live chain failed independent recomputation at index %d (of %d events)", idx, len(events))
	}
	t.Logf("live chain head at seq %d verified (INTACT+ATTESTED prefix), %d total events recomputed clean", head.Seq, len(events))

	// 4. ADR-0069 D4 (T-125b): the SAME head must also be present in the
	// out-of-Postgres anchor file — proving the live audit-service process's
	// anchor.Writer actually appended it (this is the AUD-37 live proof, not
	// merely internal/anchor's own unit tests passing in isolation). The
	// anchor write and the DB write are two independent pieces of I/O inside
	// the SAME live checkOnce call (checkpoint.go's anchorIfSigned runs
	// immediately after WithCheckpointLock returns), but this test process
	// observes them through two different reads (Postgres vs. the shared
	// auditanchor volume) with no synchronization between them, so it polls
	// rather than assuming they land in the same instant.
	anchorPath := os.Getenv("AIREOS_AUDIT_ANCHOR_PATH")
	if anchorPath == "" {
		t.Fatal("AIREOS_AUDIT_ANCHOR_PATH not set — docker-compose.e2e.yml must configure the tester with the same anchor path as the audit service (ADR-0069 D4)")
	}
	aw := anchor.New(anchorPath)

	var anchorRec anchor.Record
	found := false
	anchorDeadline := time.Now().Add(30 * time.Second)
	for {
		records, aerr := aw.All()
		if aerr != nil {
			t.Fatalf("read anchor file %s: %v", anchorPath, aerr)
		}
		for _, r := range records {
			if r.Kind == anchor.KindHead && r.Seq == head.Seq {
				anchorRec = r
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(anchorDeadline) {
			t.Fatalf("anchor file %s never recorded a head at seq %d within 30s — the live audit-service's anchor writer never ran, or disagreed", anchorPath, head.Seq)
		}
		time.Sleep(1 * time.Second)
	}

	if anchorRec.ChainHash != head.ChainHash || anchorRec.Timestamp != head.Timestamp ||
		anchorRec.KeyID != head.KeyID || anchorRec.Signature != head.Signature {
		t.Fatalf("anchor record at seq %d disagrees with the DB-signed head:\n  anchor:   %+v\n  database: seq=%d chainHash=%s timestamp=%s keyId=%s signature=%s",
			head.Seq, anchorRec, head.Seq, head.ChainHash, head.Timestamp, head.KeyID, head.Signature)
	}

	// Not just byte-identical to the DB row — independently a VALID
	// signature under the fixture public key, proving the anchor genuinely
	// carries a verifiable head rather than an opaque copy.
	anchorAsHead := chain.Head{
		Seq: anchorRec.Seq, ChainHash: anchorRec.ChainHash,
		Timestamp: anchorRec.Timestamp, KeyID: anchorRec.KeyID, Signature: anchorRec.Signature,
	}
	if err := chain.VerifyHead(pub, anchorAsHead); err != nil {
		t.Fatalf("anchor's own recorded head at seq %d failed to verify: %v", head.Seq, err)
	}

	t.Logf("live chain head at seq %d verified in BOTH Postgres and the anchor file %s — seq/chainHash/timestamp/keyId/signature all agree", head.Seq, anchorPath)
}

func chainHashAtSeq(t *testing.T, pool *pgxpool.Pool, seq int64) string {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(), `SELECT chain_hash FROM audit_events WHERE seq = $1`, seq).Scan(&hash); err != nil {
		t.Fatalf("read chain_hash at seq %d: %v", seq, err)
	}
	return hash
}

// listWholeChain pages the full audit_events table in ascending seq order —
// the same unfiltered, no-preset walk main.go's runVerify (the operator
// `verify` subcommand) performs.
func listWholeChain(t *testing.T, ctx context.Context, st *store.Store) []audit.Event {
	t.Helper()
	const page = 500
	var all []audit.Event
	var after int64
	for {
		batch, last, err := st.List(ctx, store.ListQuery{Direction: store.Asc, Cursor: after, Limit: page})
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		all = append(all, batch...)
		if len(batch) < page {
			break
		}
		after = last
	}
	return all
}
