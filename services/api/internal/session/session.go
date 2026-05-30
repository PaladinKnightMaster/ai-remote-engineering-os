// Package session holds the services/api cockpit-session domain types: the
// stored Session and the create-request DTO. They mirror the Session /
// SessionCreate schemas in the OpenAPI fragment (T-016b) and the sessions table
// CHECK constraints (migration 0002, ADR-0048 §3). The WebSocket data plane that
// activates a session lives in services/session-broker (slice b, T-017); this
// package is the REST control-plane domain only.
package session

import (
	"fmt"
	"time"
)

// Modes is the closed set of session modes. Only 'terminal' exists at v0.1.0
// (ADR-0048); the DB enforces the same set via sessions_mode_chk.
var Modes = map[string]struct{}{
	"terminal": {},
}

// DefaultMode is applied when SessionCreate omits mode.
const DefaultMode = "terminal"

// Status values (sessions_status_chk, migration 0002). A session is created
// 'pending' by services/api; the broker flips it 'active' on a successful PTY
// attach and 'closed' on teardown. 'terminated' is the forced/abnormal end.
const (
	StatusPending    = "pending"
	StatusActive     = "active"
	StatusClosed     = "closed"
	StatusTerminated = "terminated"
)

// ReasonOperator is the closed_reason set when an operator ends a session via
// the REST control plane (POST /v1/sessions/{id}/end). The broker may set
// idle|broker_crash|ttl on its own teardown paths (slice b).
const ReasonOperator = "operator"

// Session is the stored, server-completed cockpit session. endedAt/closedReason/
// castPath/castSha256 stay zero until the session is ended; omitempty keeps them
// off the wire until then, matching the OpenAPI Session schema (only id,
// resourceId, operatorId, mode, status, startedAt are required).
type Session struct {
	ID           string     `json:"id"`
	ResourceID   string     `json:"resourceId"`
	OperatorID   string     `json:"operatorId"`
	Mode         string     `json:"mode"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"startedAt"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
	ClosedReason string     `json:"closedReason,omitempty"`
	CastPath     string     `json:"castPath,omitempty"`
	CastSha256   string     `json:"castSha256,omitempty"`
}

// CreateInput is the POST /v1/sessions request body. id, operatorId and status
// are server-assigned, so they are not part of the input; mode defaults to the
// only v0.1.0 value via Normalize.
type CreateInput struct {
	ResourceID string `json:"resourceId"`
	Mode       string `json:"mode,omitempty"`
}

// Normalize fills server-side defaults before Validate. Called after decode.
func (in *CreateInput) Normalize() {
	if in.Mode == "" {
		in.Mode = DefaultMode
	}
}

// Validate checks required fields and the mode enum at the API boundary so a
// malformed request fails with 400 rather than as a DB CHECK error.
func (in CreateInput) Validate() error {
	if in.ResourceID == "" {
		return fmt.Errorf("resourceId is required")
	}
	if _, ok := Modes[in.Mode]; !ok {
		return fmt.Errorf("mode %q is not a valid session mode", in.Mode)
	}
	return nil
}
