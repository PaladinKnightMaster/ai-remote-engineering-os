package outbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeTx captures the Exec call so the Writer's SQL + payload contract can be
// asserted without a database. Embedding pgx.Tx (nil) satisfies the interface;
// only Exec is exercised.
type fakeTx struct {
	pgx.Tx
	sql  string
	args []any
}

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql = sql
	f.args = args
	return pgconn.CommandTag{}, nil
}

// ingestShape mirrors audit-service's POST /v1/events body. The stored payload
// must unmarshal cleanly into it (one schema, verbatim relay POST).
type ingestShape struct {
	Timestamp        string `json:"timestamp"`
	Actor            string `json:"actor"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Outcome          string `json:"outcome,omitempty"`
	EvidencePacketID string `json:"evidencePacketId,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

func TestEnqueueWritesAuditIngestPayload(t *testing.T) {
	tx := &fakeTx{}
	e := Event{
		Timestamp:        time.Date(2026, 5, 30, 1, 2, 3, 0, time.UTC),
		Actor:            "operator:admin",
		Edge:             "agent-remote-session",
		Target:           "session:abc",
		Outcome:          "opened",
		EvidencePacketID: "sha256:deadbeef",
	}
	key, err := Writer{}.Enqueue(context.Background(), tx, e)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Idempotency key is a UUID v7 (time-ordered).
	u, err := uuid.Parse(key)
	if err != nil || u.Version() != 7 {
		t.Fatalf("idempotency key not UUID v7: %q (err=%v)", key, err)
	}

	// The table identifier is now sanitized/quoted (pgx.Identifier), default "outbox".
	if !strings.Contains(tx.sql, `INSERT INTO "outbox"`) {
		t.Fatalf("unexpected SQL: %q", tx.sql)
	}
	if len(tx.args) != 2 || tx.args[0] != key {
		t.Fatalf("unexpected args: %#v", tx.args)
	}
	payload, ok := tx.args[1].(string)
	if !ok {
		t.Fatalf("payload arg is %T, want string", tx.args[1])
	}

	var got ingestShape
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload is not valid audit-ingest JSON: %v\n%s", err, payload)
	}
	want := ingestShape{
		Timestamp: "2026-05-30T01:02:03Z", Actor: "operator:admin",
		Edge: "agent-remote-session", Target: "session:abc",
		Outcome: "opened", EvidencePacketID: "sha256:deadbeef", IdempotencyKey: key,
	}
	if got != want {
		t.Fatalf("payload mismatch:\n got  %+v\n want %+v", got, want)
	}
}

// Optional fields must be omitted (not null/empty) so the audit Ingest decoder's
// DisallowUnknownFields posture and omitempty expectations hold.
func TestEnqueueOmitsEmptyOptionalFields(t *testing.T) {
	tx := &fakeTx{}
	if _, err := (Writer{}).Enqueue(context.Background(), tx, Event{
		Timestamp: time.Now(), Actor: "svc:relay", Edge: "service-service", Target: "x",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	payload := tx.args[1].(string)
	if strings.Contains(payload, "outcome") || strings.Contains(payload, "evidencePacketId") {
		t.Fatalf("empty optional fields should be omitted: %s", payload)
	}
}
