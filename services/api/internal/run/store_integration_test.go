//go:build integration

// Integration tests for the Run store against a real Postgres.
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/run/...
package run

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
	t.Cleanup(pool.Close)
	return NewStore(pool, nil, "api_outbox") // nil sealer → plaintext; Create enqueues run.launched into api_outbox
}

// TestRunStoreCreateGetList — the T-054 G1 live-PG proof: a Run + its ordered steps
// persist in one transaction; Get returns the run with its steps in execution order
// (commands round-tripped through the BYTEA column, defaults set); List is run-level
// (no steps); an unknown id is ErrNotFound; deleting the run cascades its steps.
func TestRunStoreCreateGetList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	in := Input{
		Operator: "operator:admin",
		Steps: []StepInput{
			{ResourceID: "res-a", Command: "uname -a"},
			{ResourceID: "res-b", Command: "df -h"},
			{ResourceID: "res-c", Command: "echo RUN-OK"},
		},
	}
	created, err := s.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Status != StatusPending || len(created.Steps) != 3 {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Operator != "operator:admin" || got.Status != StatusPending {
		t.Fatalf("get run mismatch: %+v", got)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(got.Steps))
	}
	wantCmd := []string{"uname -a", "df -h", "echo RUN-OK"}
	for i, st := range got.Steps {
		if st.Index != i {
			t.Fatalf("step out of order at %d: index=%d", i, st.Index)
		}
		if st.Command != wantCmd[i] {
			t.Fatalf("step %d command: got %q want %q", i, st.Command, wantCmd[i])
		}
		if st.Status != StepPending || st.InvocationID != "" || st.Outcome != "" {
			t.Fatalf("step %d defaults wrong: %+v", i, st)
		}
	}

	// List is run-level (no steps populated) and includes the created run.
	runs, _, err := s.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, r := range runs {
		if r.ID == created.ID {
			found = true
			if len(r.Steps) != 0 {
				t.Fatalf("list should not populate steps, got %d", len(r.Steps))
			}
		}
	}
	if !found {
		t.Fatalf("created run %s not in list", created.ID)
	}

	// Unknown id → ErrNotFound.
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}

	// FK cascade: deleting the run removes its steps.
	var stepCount int
	if _, err := s.pool.Exec(ctx, `DELETE FROM runs WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM run_steps WHERE run_id = $1`, created.ID).Scan(&stepCount); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if stepCount != 0 {
		t.Fatalf("cascade failed: %d steps remain", stepCount)
	}
}

// TestRunStoreCreateValidation — Create rejects an empty plan and a step missing a
// resource or command (fail-loud, no partial run written).
func TestRunStoreCreateValidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, Input{Operator: "operator:admin"}); err == nil {
		t.Fatal("empty plan: want error, got nil")
	}
	if _, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{ResourceID: "res-a"}}}); err == nil {
		t.Fatal("step missing command: want error, got nil")
	}
	if _, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{Command: "echo hi"}}}); err == nil {
		t.Fatal("step missing resource: want error, got nil")
	}
}

// TestRunClaimLease — T-056 DUR-2: ClaimRun is reclaimable. It takes a pending Run or
// a 'running' Run whose lease has expired, but NOT one holding a live lease;
// NextClaimable mirrors that, and RenewClaim pushes the lease forward.
func TestRunClaimLease(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{ResourceID: "r-a", Command: "echo hi"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.ID

	// Fresh pending → claimable, and the claim wins.
	if !inClaimable(t, s, id) {
		t.Fatal("a pending run must be claimable")
	}
	if ok, err := s.ClaimRun(ctx, id, 1200); err != nil || !ok {
		t.Fatalf("claim pending: ok=%v err=%v", ok, err)
	}

	// Now running with a live lease → not claimable, and a second claim loses.
	if inClaimable(t, s, id) {
		t.Fatal("a live-leased run must not be claimable")
	}
	if ok, _ := s.ClaimRun(ctx, id, 1200); ok {
		t.Fatal("claimed a live-leased run (double-drive)")
	}

	// Expire the lease (a crashed driver) → reclaimable, and the reclaim wins.
	if _, err := s.pool.Exec(ctx, `UPDATE runs SET claimed_until = now() - interval '1 minute' WHERE id = $1`, id); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if !inClaimable(t, s, id) {
		t.Fatal("a stale-leased run must be reclaimable")
	}
	if ok, _ := s.ClaimRun(ctx, id, 1200); !ok {
		t.Fatal("failed to reclaim a stale-leased run")
	}

	// RenewClaim pushes the lease into the future → not claimable again.
	if err := s.RenewClaim(ctx, id, 1200); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if inClaimable(t, s, id) {
		t.Fatal("a renewed run must not be claimable")
	}
}

func inClaimable(t *testing.T, s *Store, id string) bool {
	t.Helper()
	ids, err := s.NextClaimable(context.Background(), 1000)
	if err != nil {
		t.Fatalf("next claimable: %v", err)
	}
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
