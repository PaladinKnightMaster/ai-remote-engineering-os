// Package store is the session-broker's Postgres access layer. It reads the
// session + target a WebSocket channel needs, transitions session lifecycle
// state, and — in the SAME transaction as each transition — enqueues the
// session.open / session.close evidence into the outbox (ADR-0045), which the
// relay drains into the audit-service chain authority. This couples the state
// change and its evidence atomically (no dual-write).
//
// The audit model is lifecycle-chain + cast-anchored (consult 2026-05-30): only
// session.open and session.close hit the chain; per-line detail lives in the
// asciinema cast file, made tamper-evident by the cast SHA256 anchored in the
// session.close event (ADR-0048 §SEC-11).
//
// NOTE (debt): the target-secret decrypt mirrors services/api/internal/secret
// (the Material JSON shape + the pgp_sym_decrypt query). That package is api's
// internal/, so it cannot be imported across the module boundary. The shared
// contract is small and stable; extracting a packages/go/secret is the follow-up
// when a third consumer appears.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

// Sentinel errors mapped to WebSocket close codes by the channel handler.
var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrNotActivatable   = errors.New("session is not in an activatable/closable state")
	ErrNoSecret         = errors.New("resource has no stored connection secret")
	ErrTargetIncomplete = errors.New("resource is missing required ssh_* labels")
)

// auditEdge is the capability edge for cockpit remote-session evidence. It is the
// data-plane edge the broker's WS upgrade is gated on, and a recognized edge in
// the audit chain.
const auditEdge = "agent-remote-session"

// Session is the broker's view of a sessions row.
type Session struct {
	ID         string
	ResourceID string
	OperatorID string
	Status     string
	Mode       string
}

// Target is everything needed to dial the SSH target: non-secret coordinates from
// resources.labels plus the decrypted private key.
type Target struct {
	Host       string
	Port       string
	User       string
	HostKey    string // authorized_keys line, pinned via ssh.FixedHostKey (labels ssh_host_key)
	PrivateKey string // decrypted PEM
	Passphrase string
}

// material mirrors services/api/internal/secret.Material (the encrypted blob).
type material struct {
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Store wraps the broker's pgx pool and the shared outbox Writer.
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// LoadSession returns the broker's view of a session, or ErrSessionNotFound.
func (s *Store) LoadSession(ctx context.Context, id string) (Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx,
		`SELECT id, resource_id, operator_id, status, mode FROM sessions WHERE id = $1`, id).
		Scan(&sess.ID, &sess.ResourceID, &sess.OperatorID, &sess.Status, &sess.Mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("load session: %w", err)
	}
	return sess, nil
}

// LoadTarget reads the SSH connection coordinates from resources.labels and
// decrypts the stored private key with secretKey (AIREOS_RESOURCE_SECRET_KEY).
// ErrNoSecret if no secret is provisioned; ErrTargetIncomplete if a required
// label is missing. Host-key pinning is mandatory: ssh_host_key must be present.
func (s *Store) LoadTarget(ctx context.Context, resourceID, secretKey string) (Target, error) {
	var labelsRaw, secretBlob []byte
	err := s.pool.QueryRow(ctx, `
		SELECT labels, pgp_sym_decrypt(connection_secret, $2)
		  FROM resources
		 WHERE id = $1 AND connection_secret IS NOT NULL`, resourceID, secretKey).
		Scan(&labelsRaw, &secretBlob)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNoSecret
	}
	if err != nil {
		return Target{}, fmt.Errorf("load+decrypt target: %w", err)
	}

	labels := map[string]string{}
	if len(labelsRaw) > 0 {
		if err := json.Unmarshal(labelsRaw, &labels); err != nil {
			return Target{}, fmt.Errorf("unmarshal labels: %w", err)
		}
	}
	var m material
	if err := json.Unmarshal(secretBlob, &m); err != nil {
		return Target{}, fmt.Errorf("unmarshal material: %w", err)
	}

	t := Target{
		Host:       labels["ssh_host"],
		Port:       orDefault(labels["ssh_port"], "22"),
		User:       labels["ssh_user"],
		HostKey:    labels["ssh_host_key"],
		PrivateKey: m.PrivateKey,
		Passphrase: m.Passphrase,
	}
	if t.Host == "" || t.User == "" || t.HostKey == "" || t.PrivateKey == "" {
		return Target{}, ErrTargetIncomplete
	}
	return t, nil
}

// Activate transitions a pending session (owned by operatorID) to active and
// enqueues session.open in the same transaction. ErrNotActivatable if the session
// is not pending or not owned by the caller.
func (s *Store) Activate(ctx context.Context, sessionID, operatorID string) error {
	_, err := s.txEnqueue(ctx,
		func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx,
				`UPDATE sessions SET status = 'active'
				  WHERE id = $1 AND operator_id = $2 AND status = 'pending'`,
				sessionID, operatorID)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrNotActivatable
			}
			return nil
		},
		outbox.Event{
			Timestamp: time.Now(), Actor: operatorID, Edge: auditEdge,
			Target: "session:" + sessionID, Outcome: "opened",
		})
	return err
}

// Close transitions an open session to closed, stamps ended_at/closed_reason and
// the cast path+SHA, and enqueues session.close (with the cast SHA anchored in
// evidencePacketId) in the same transaction. Idempotent at the handler level:
// ErrNotActivatable if the session is already closed/terminated.
func (s *Store) Close(ctx context.Context, sessionID, operatorID, reason, castPath, castSHA string) error {
	evidence := ""
	if castSHA != "" {
		evidence = "sha256:" + castSHA
	}
	_, err := s.txEnqueue(ctx,
		func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, `
				UPDATE sessions
				   SET status = 'closed', ended_at = now(), closed_reason = $2,
				       cast_path = NULLIF($3, ''), cast_sha256 = NULLIF($4, '')
				 WHERE id = $1 AND status IN ('pending', 'active')`,
				sessionID, reason, castPath, castSHA)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrNotActivatable
			}
			return nil
		},
		outbox.Event{
			Timestamp: time.Now(), Actor: operatorID, Edge: auditEdge,
			Target: "session:" + sessionID, Outcome: "closed:" + reason,
			EvidencePacketID: evidence,
		})
	return err
}

// RecordInputLine emits a terminal.input.line audit event for one Enter-delimited
// operator line (ADR-0048 §6). Per the T-019a decision, the chain carries only
// the line's SHA256 (evidencePacketId) — the raw bytes live in the access-
// controlled cast file — so per-line tamper-evidence holds without persisting
// typed secrets into the immutable chain. No session-state change, so the
// enqueue stands alone (no-op mutate).
func (s *Store) RecordInputLine(ctx context.Context, sessionID, operatorID, lineSHA string) error {
	_, err := s.txEnqueue(ctx,
		func(pgx.Tx) error { return nil },
		outbox.Event{
			Timestamp: time.Now(), Actor: operatorID, Edge: auditEdge,
			Target: "session:" + sessionID, Outcome: "input.line",
			EvidencePacketID: "sha256:" + lineSHA,
		})
	return err
}

// txEnqueue runs mutate and an outbox Enqueue in one transaction, so the state
// change and its evidence commit atomically (ADR-0045, no dual-write).
func (s *Store) txEnqueue(ctx context.Context, mutate func(pgx.Tx) error, e outbox.Event) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed
	if err := mutate(tx); err != nil {
		return "", err
	}
	key, err := s.writer.Enqueue(ctx, tx, e)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit lifecycle tx: %w", err)
	}
	return key, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
