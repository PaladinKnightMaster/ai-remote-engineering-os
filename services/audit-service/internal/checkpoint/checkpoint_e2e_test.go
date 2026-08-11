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
package checkpoint

import (
	"context"
	"crypto/ed25519"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
