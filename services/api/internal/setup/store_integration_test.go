//go:build integration

// Integration tests for the first-run setup store against a real Postgres (T-175a,
// SEC-64 at the store). The tables under test hold a SINGLETON (one operator, one live
// code), so unlike the unique-suffix suites these tests reset them: every test starts
// from, and leaves behind, empty setup_codes + operators + setup.* outbox rows. Only this
// package touches those tables, and a package's tests run serially.
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/setup/...
package setup

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const goodPassword = "correct horse battery"

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("API_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	reset := func() {
		_, err := pool.Exec(context.Background(),
			`TRUNCATE operators, setup_codes; DELETE FROM api_outbox WHERE payload->>'outcome' LIKE 'setup.%'`)
		if err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	reset()
	t.Cleanup(func() { reset(); pool.Close() })
	return NewStore(pool, "api_outbox")
}

func mustIssue(t *testing.T, s *Store) string {
	t.Helper()
	code, err := s.Issue(context.Background())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return code
}

func count(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// outboxPayloads returns every setup.* audit payload, oldest first, as raw JSON text.
func outboxPayloads(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.pool.Query(context.Background(),
		`SELECT payload::text FROM api_outbox WHERE payload->>'outcome' LIKE 'setup.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestSetupHappyPath(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	code := mustIssue(t, s)

	// Typed sloppily -- lower case, spaces for dashes -- as an operator would.
	op, err := s.Complete(ctx, strings.ToLower(strings.ReplaceAll(code, "-", " ")), "alice", goodPassword, "203.0.113.9")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if subj, ok := op.Authenticate("alice", goodPassword); !ok || subj != "operator:alice" {
		t.Fatalf("authenticate = %q, %v", subj, ok)
	}
	if _, ok := op.Authenticate("alice", "wrong password!!"); ok {
		t.Fatal("wrong password authenticated")
	}
	loaded, err := s.Operator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Authenticate("alice", goodPassword); !ok {
		t.Fatal("the stored operator does not authenticate")
	}
	if n := count(t, s, `SELECT count(*) FROM operators WHERE password_hash LIKE '$argon2id$%' AND password_hash NOT LIKE '%`+goodPassword+`%'`); n != 1 {
		t.Fatalf("operator row is not a single argon2id hash (got %d)", n)
	}
	if n := count(t, s, `SELECT count(*) FROM setup_codes WHERE consumed_at IS NOT NULL`); n != 1 {
		t.Fatalf("code not consumed in the completing transaction (consumed rows = %d)", n)
	}
	events := outboxPayloads(t, s)
	if len(events) != 2 || !strings.Contains(events[0], `"setup.code.issued"`) ||
		!strings.Contains(events[1], `"setup.completed"`) ||
		!strings.Contains(events[1], `"actor": "operator:alice"`) ||
		!strings.Contains(events[1], `setup-client:203.0.113.9`) {
		t.Fatalf("unexpected audit events: %v", events)
	}
}

// SEC-64: the attacker's side. A wrong code is refused and creates nothing.
func TestSetupWrongCodeRefused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	code := mustIssue(t, s)

	if _, err := s.Complete(ctx, "00000-00000-00000-00000", "mallory", goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: err = %v, want ErrInvalidCode", err)
	}
	if n := count(t, s, `SELECT count(*) FROM operators`); n != 0 {
		t.Fatal("a wrong code created an operator")
	}
	if n := count(t, s, `SELECT attempts FROM setup_codes`); n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	// A weak password with the RIGHT code is an input error: it counts nothing and burns nothing.
	var ie *InputError
	if _, err := s.Complete(ctx, code, "alice", "short", ""); !errors.As(err, &ie) {
		t.Fatalf("weak password: err = %v, want *InputError", err)
	}
	if n := count(t, s, `SELECT attempts FROM setup_codes WHERE consumed_at IS NULL`); n != 1 {
		t.Fatalf("an input error moved attempts to %d", n)
	}
	if _, err := s.Complete(ctx, code, "alice", goodPassword, ""); err != nil {
		t.Fatalf("the code should still work after one wrong guess: %v", err)
	}
}

// SEC-64: five wrong attempts invalidate the code; the sixth, with the RIGHT code, is refused.
func TestSetupLockoutAfterFiveWrongAttempts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	code := mustIssue(t, s)

	for i := 1; i <= 5; i++ { // literal: ADR-0078 says five
		if _, err := s.Complete(ctx, "ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ", "mallory", goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("wrong attempt %d: err = %v", i, err)
		}
	}
	if _, err := s.Complete(ctx, code, "alice", goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("right code after lockout: err = %v, want ErrInvalidCode", err)
	}
	if n := count(t, s, `SELECT count(*) FROM operators`); n != 0 {
		t.Fatal("a locked code created an operator")
	}
	if n := count(t, s, `SELECT count(*) FROM setup_codes WHERE invalidated_at IS NOT NULL AND consumed_at IS NULL AND attempts = 5`); n != 1 {
		t.Fatal("the fifth failure did not invalidate the code in the same commit as the increment")
	}
	var locked int
	for _, e := range outboxPayloads(t, s) {
		if strings.Contains(e, `"setup.code.locked"`) {
			locked++
		}
	}
	if locked != 1 {
		t.Fatalf("setup.code.locked emitted %d times, want exactly 1", locked)
	}
}

// SEC-64: a used code is single-use. Replay is checked twice -- once as it really
// happens (operator exists), once with the operator row removed so ONLY the consumed
// code can be what refuses it.
func TestSetupReplayRefused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	code := mustIssue(t, s)
	if _, err := s.Complete(ctx, code, "alice", goodPassword, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, code, "mallory", goodPassword, ""); !errors.Is(err, ErrOperatorExists) {
		t.Fatalf("replay: err = %v, want ErrOperatorExists", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM operators`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, code, "mallory", goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("replay with no operator: err = %v, want ErrInvalidCode (consumed_at)", err)
	}
}

// SEC-64: an expired code is refused (clock injected); one second earlier it is not.
func TestSetupExpiredCodeRefused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	issuedAt := time.Now()
	s.now = func() time.Time { return issuedAt }
	code := mustIssue(t, s)

	s.now = func() time.Time { return issuedAt.Add(CodeLifetime) }
	if _, err := s.Complete(ctx, code, "alice", goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("at expiry: err = %v, want ErrInvalidCode", err)
	}
	s.now = func() time.Time { return issuedAt.Add(CodeLifetime - time.Second) }
	if _, err := s.Complete(ctx, code, "alice", goodPassword, ""); err != nil {
		t.Fatalf("one second before expiry: %v", err)
	}
}

// ADR-0078 §D1: re-issue invalidates the previous code; at most one is ever live.
func TestSetupReissueInvalidatesOldCode(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	oldCode := mustIssue(t, s)
	newCode := mustIssue(t, s)

	if _, err := s.Complete(ctx, oldCode, "alice", goodPassword, ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("old code after re-issue: err = %v, want ErrInvalidCode", err)
	}
	if n := count(t, s, `SELECT count(*) FROM setup_codes WHERE consumed_at IS NULL AND invalidated_at IS NULL`); n != 1 {
		t.Fatalf("live codes = %d, want 1", n)
	}
	if _, err := s.Complete(ctx, newCode, "alice", goodPassword, ""); err != nil {
		t.Fatalf("new code: %v", err)
	}
}

func TestSetupIssueRefusedOnceOperatorExists(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Complete(ctx, mustIssue(t, s), "alice", goodPassword, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue(ctx); !errors.Is(err, ErrOperatorExists) {
		t.Fatalf("issue after setup: err = %v, want ErrOperatorExists", err)
	}
	if n := count(t, s, `SELECT count(*) FROM setup_codes WHERE consumed_at IS NULL AND invalidated_at IS NULL`); n != 0 {
		t.Fatal("a refused issue left a live code behind")
	}
}

// ADR-0078 §D4: "at most one operator" is the DATABASE's guarantee, so raw SQL -- which
// no Go validation sits in front of -- cannot add a second, nor a second live code.
func TestSetupDatabaseEnforcesSingletons(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ins := `INSERT INTO operators (username, password_hash) VALUES ($1, 'x')`
	if _, err := s.pool.Exec(ctx, ins, "first"); err != nil {
		t.Fatalf("first operator row: %v", err)
	}
	if _, err := s.pool.Exec(ctx, ins, "second"); err == nil {
		t.Fatal("DB accepted a second operator row")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO operators (singleton, username, password_hash) VALUES (false, 'third', 'x')`); err == nil {
		t.Fatal("DB accepted an operator row with singleton=false")
	}
	live := `INSERT INTO setup_codes (id, code_hash, expires_at) VALUES (gen_random_uuid(), '\x00', now() + interval '1 hour')`
	if _, err := s.pool.Exec(ctx, live); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, live); err == nil {
		t.Fatal("DB accepted two live setup codes")
	}
}

// Two simultaneous completions with the RIGHT code: exactly one operator.
func TestSetupConcurrentCompletionOneWinner(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	code := mustIssue(t, s)

	const racers = 8
	errs := make([]error, racers)
	var wg sync.WaitGroup
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Complete(ctx, code, "racer"+string(rune('a'+i)), goodPassword, "")
		}(i)
	}
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrInvalidCode), errors.Is(err, ErrOperatorExists):
		default:
			t.Fatalf("racer %d failed uncleanly: %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent completions succeeded, want exactly 1", wins)
	}
	if n := count(t, s, `SELECT count(*) FROM operators`); n != 1 {
		t.Fatalf("operators = %d, want 1", n)
	}
	completed := 0
	for _, e := range outboxPayloads(t, s) {
		if strings.Contains(e, `"setup.completed"`) {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("setup.completed emitted %d times, want 1", completed)
	}
}

// leaks reports which secrets appear in any payload: the code in its typed and
// normalised spellings, and the stored hash as hex or base64 -- the ways it could be
// serialised into an audit event.
func leaks(payloads, secrets []string) []string {
	var found []string
	for _, p := range payloads {
		for _, sec := range secrets {
			if sec != "" && strings.Contains(p, sec) {
				found = append(found, sec)
			}
		}
	}
	return found
}

// SEC-64/SEC-67 (store half): no audit event carries the code or its hash. An absence
// check proves nothing until it is seen to FIND the secret, so the same check is first
// run against an event with the code planted in it.
func TestSetupAuditEventsCarryNoCodeOrHash(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	lockedCode := mustIssue(t, s) // gets locked out: setup.code.locked
	for i := 0; i < 5; i++ {
		_, _ = s.Complete(ctx, "ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ", "mallory", goodPassword, "")
	}
	finalCode := mustIssue(t, s) // completes: setup.completed
	var rawHash []byte
	if err := s.pool.QueryRow(ctx, `SELECT code_hash FROM setup_codes WHERE consumed_at IS NULL AND invalidated_at IS NULL`).Scan(&rawHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, finalCode, "alice", goodPassword, ""); err != nil {
		t.Fatal(err)
	}

	secrets := []string{
		lockedCode, Normalize(lockedCode), finalCode, Normalize(finalCode),
		hex.EncodeToString(rawHash), base64.StdEncoding.EncodeToString(rawHash),
		base64.RawStdEncoding.EncodeToString(rawHash), base64.URLEncoding.EncodeToString(rawHash),
		goodPassword,
	}

	// Positive control: plant the code in an event and require the check to find it.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.emit(ctx, tx, "system:setup", "setup-code:"+finalCode, "setup.planted"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if f := leaks(outboxPayloads(t, s), secrets); len(f) == 0 {
		t.Fatal("PLANTED CONTROL FAILED: the leak check did not find a code planted in an event")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM api_outbox WHERE payload->>'outcome' = 'setup.planted'`); err != nil {
		t.Fatal(err)
	}

	// The real run: events exist (so the absence is not an empty corpus) and carry nothing.
	events := outboxPayloads(t, s)
	if len(events) != 4 { // issued (locked-out code), locked, issued (final code), completed
		t.Fatalf("expected 4 setup.* events, got %d: %v", len(events), events)
	}
	if f := leaks(events, secrets); len(f) != 0 {
		t.Fatalf("an audit event carries a secret: %v", f)
	}
}

// Sanity for the transaction helper: a failing fn leaves nothing behind.
func TestInTxRollsBackOnError(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	boom := errors.New("boom")
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO operators (username, password_hash) VALUES ('ghost', 'x')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || count(t, s, `SELECT count(*) FROM operators`) != 0 {
		t.Fatalf("rollback failed: err=%v", err)
	}
}

// Concurrent WRONG guesses are all counted: the row lock stops two of them reading the
// same attempts value and both writing +1 (a lost update would let an attacker's
// parallel guesses outrun the lockout).
func TestSetupConcurrentWrongGuessesAllCounted(t *testing.T) {
	s := testStore(t)
	mustIssue(t, s)
	const guesses = 4 // below the cap, so the row stays live and countable
	var wg sync.WaitGroup
	wg.Add(guesses)
	for i := 0; i < guesses; i++ {
		go func() {
			defer wg.Done()
			_, _ = s.Complete(context.Background(), "ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ", "mallory", goodPassword, "")
		}()
	}
	wg.Wait()
	if n := count(t, s, `SELECT attempts FROM setup_codes WHERE consumed_at IS NULL AND invalidated_at IS NULL`); n != guesses {
		t.Fatalf("attempts = %d after %d concurrent wrong guesses (lost update)", n, guesses)
	}
}
