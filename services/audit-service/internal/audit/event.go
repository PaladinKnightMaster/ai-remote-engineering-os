// Package audit holds the audit-service domain types: the stored AuditEvent and
// the POST /v1/events ingest DTO. These mirror packages/shared-schemas/
// audit-event.schema.json (T-003) and the AuditEventIngest contract schema
// (T-004b). They are hand-authored because oapi-codegen could not run in the
// authoring environment (toolchain gap, see oapi-codegen.yaml); regenerating
// the models via `task codegen` should produce equivalent shapes.
package audit

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Edges is the closed set of capability edges (docs/security/agent-permission-model.md
// plus service-service, ADR-0046). The DB enforces the same set via a CHECK.
var Edges = map[string]struct{}{
	"human-agent":          {},
	"agent-agent":          {},
	"agent-tool":           {},
	"agent-workspace":      {},
	"agent-repo":           {},
	"agent-memory":         {},
	"agent-remote-session": {},
	"service-service":      {},
}

// Ingest is the POST /v1/events request body. Producers supply only semantic
// fields; the chain authority assigns id, prevHash, and chainHash. The decoder
// rejects unknown fields, so a client-supplied prevHash/chainHash/id is a 400.
type Ingest struct {
	Timestamp        string `json:"timestamp"`
	Actor            string `json:"actor"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Outcome          string `json:"outcome,omitempty"`
	EvidencePacketID string `json:"evidencePacketId,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey"`
}

// Validate checks the required fields, formats, and the edge enum at the API
// boundary so a malformed request fails with 400 rather than surfacing as a DB
// error later. It does NOT check for forbidden chain fields — that is the
// decoder's job (DisallowUnknownFields).
func (in Ingest) Validate() error {
	switch {
	case in.Timestamp == "":
		return fmt.Errorf("timestamp is required")
	case in.Actor == "":
		return fmt.Errorf("actor is required")
	case in.Target == "":
		return fmt.Errorf("target is required")
	case in.IdempotencyKey == "":
		return fmt.Errorf("idempotencyKey is required")
	}
	if _, err := uuid.Parse(in.IdempotencyKey); err != nil {
		return fmt.Errorf("idempotencyKey must be a UUID: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, in.Timestamp); err != nil {
		return fmt.Errorf("timestamp must be RFC3339: %w", err)
	}
	if _, ok := Edges[in.Edge]; !ok {
		return fmt.Errorf("edge %q is not a recognised capability edge", in.Edge)
	}
	return nil
}

// Event is the stored, server-completed audit event (the full AuditEvent
// contract type). Seq is the monotonic chain order; it is internal and not part
// of the wire schema.
type Event struct {
	Seq              int64  `json:"-"`
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	Actor            string `json:"actor"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Outcome          string `json:"outcome,omitempty"`
	EvidencePacketID string `json:"evidencePacketId,omitempty"`
	IdempotencyKey   string `json:"idempotencyKey"`
	PrevHash         string `json:"prevHash"`
	ChainHash        string `json:"chainHash"`
}
