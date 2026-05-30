// Package outbox is the shared Postgres transactional-outbox writer + relay
// (ADR-0045), the third shared-infra module after packages/go/middleware
// (ADR-0043) and packages/go/migrate (ADR-0044) and following the same shape.
//
// A producer service enqueues an evidence Event into its local `outbox` table
// inside the SAME transaction as its state change (Writer.Enqueue), which
// eliminates the dual-write problem by construction. A Relay drains the table
// and POSTs each row to the chain authority (services/audit-service
// POST /v1/events), marking it delivered on a 200. Delivery is at-least-once;
// audit-service dedupes on the UUID v7 idempotency key the Writer mints.
//
// The canonical `outbox` table DDL (table + UNIQUE idempotency_key + partial
// undelivered index + pg_notify wakeup trigger) is documented in README.md and
// instantiated per service via that service's own migrations (ADR-0044).
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// table is the per-service outbox table name. Every producer shares the same
// name (each in its own migration), so it is a constant, not configurable —
// which also keeps it out of any string-built SQL as an injection surface.
const table = "outbox"

// Event is the producer-supplied semantic payload of an audit evidence packet.
// It maps 1:1 onto audit-service's POST /v1/events Ingest body minus the
// idempotency key (the Writer mints that). The chain authority assigns
// id/prevHash/chainHash; producers never supply chain fields.
type Event struct {
	Timestamp        time.Time // event time; serialized RFC3339 UTC
	Actor            string    // capability subject (e.g. "operator:admin")
	Edge             string    // capability edge (e.g. "agent-remote-session")
	Target           string    // the audited object (e.g. "session:<id>")
	Outcome          string    // optional ("success", "closed:idle", ...)
	EvidencePacketID string    // optional pointer to out-of-band evidence (e.g. a cast SHA256)
}

// wireEvent is the exact JSON shape audit-service's Ingest decoder accepts. The
// stored outbox payload IS this JSON, so the Relay can POST the row bytes
// verbatim with no re-marshal.
type wireEvent struct {
	Timestamp        string `json:"timestamp"`
	Actor            string `json:"actor"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Outcome          string `json:"outcome,omitempty"`
	EvidencePacketID string `json:"evidencePacketId,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

// Writer enqueues evidence rows. It is stateless; a zero Writer is ready to use.
type Writer struct{}

// Enqueue writes one pending evidence row inside tx (a pgx.Tx the caller obtained
// for its own state change). The idempotency key is a UUID v7 minted here and
// returned so the caller can correlate. The row's payload is the audit-ingest
// JSON, ready for the Relay to POST verbatim.
func (Writer) Enqueue(ctx context.Context, tx pgx.Tx, e Event) (string, error) {
	key, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("outbox: mint idempotency key: %w", err)
	}
	payload, err := json.Marshal(wireEvent{
		Timestamp:        e.Timestamp.UTC().Format(time.RFC3339),
		Actor:            e.Actor,
		Edge:             e.Edge,
		Target:           e.Target,
		Outcome:          e.Outcome,
		EvidencePacketID: e.EvidencePacketID,
		IdempotencyKey:   key.String(),
	})
	if err != nil {
		return "", fmt.Errorf("outbox: marshal payload: %w", err)
	}
	const q = `INSERT INTO ` + table + ` (idempotency_key, payload) VALUES ($1, $2::jsonb)`
	if _, err := tx.Exec(ctx, q, key.String(), string(payload)); err != nil {
		return "", fmt.Errorf("outbox: enqueue: %w", err)
	}
	return key.String(), nil
}
