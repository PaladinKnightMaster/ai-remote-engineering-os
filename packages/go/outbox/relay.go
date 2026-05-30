package outbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Client delivers one already-serialized audit-ingest payload to the chain
// authority. HTTPClient is the production implementation; tests substitute a
// fake. The payload is the verbatim jsonb row bytes the Writer stored, so the
// Relay never re-marshals.
type Client interface {
	Append(ctx context.Context, payload []byte) error
}

// Relay drains the local outbox table and delivers pending rows to the chain
// authority, marking each delivered on success (at-least-once; the sink dedupes
// on idempotency_key, ADR-0045). It wakes on a Postgres LISTEN/NOTIFY hint and
// falls back to a periodic sweep so a missed notification cannot strand rows.
type Relay struct {
	Pool   *pgxpool.Pool
	Client Client

	// Channel is the LISTEN/NOTIFY wakeup channel (default "outbox_new"). It is
	// only a hint — the transport is HTTP, never NOTIFY (ADR-0045).
	Channel string
	// SweepInterval is the periodic fallback drain cadence (default 10s).
	SweepInterval time.Duration
	// BatchSize caps rows claimed per batch (default 100).
	BatchSize int
	// Logf logs transient, self-healing errors (default log.Printf).
	Logf func(string, ...any)
}

func (r *Relay) applyDefaults() {
	if r.Channel == "" {
		r.Channel = "outbox_new"
	}
	if r.SweepInterval <= 0 {
		r.SweepInterval = 10 * time.Second
	}
	if r.BatchSize <= 0 {
		r.BatchSize = 100
	}
	if r.Logf == nil {
		r.Logf = log.Printf
	}
}

// Run drains on startup (to catch rows enqueued before the listener attached),
// then loops: drain, then block until a NOTIFY wakeup or the sweep tick. Returns
// ctx.Err() on cancellation. Transient drain/listen errors are logged and
// retried, never fatal — a degraded chain authority surfaces as growing outbox
// depth, not a crashed relay (ADR-0045).
func (r *Relay) Run(ctx context.Context) error {
	r.applyDefaults()

	wake := make(chan struct{}, 1)
	go r.listen(ctx, wake)

	ticker := time.NewTicker(r.SweepInterval)
	defer ticker.Stop()

	for {
		if _, err := r.drain(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.Logf("outbox relay: drain: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-wake:
		}
	}
}

// drain repeatedly claims batches until the table is empty (a short batch means
// no more pending rows), so a NOTIFY storm or a large backlog is cleared in one
// wakeup.
func (r *Relay) drain(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := r.drainBatch(ctx)
		total += n
		if err != nil {
			return total, err
		}
		if n < r.BatchSize {
			return total, nil
		}
	}
}

// drainBatch claims up to BatchSize undelivered rows with FOR UPDATE SKIP LOCKED
// (so a second relay instance — e.g. the operator-run `outbox relay` subcommand
// alongside the in-process one — processes disjoint rows), delivers each, and
// marks them delivered in the same transaction. A delivery failure rolls the
// whole batch back; already-delivered rows are re-POSTed on retry and deduped at
// the sink (at-least-once).
func (r *Relay) drainBatch(ctx context.Context) (int, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	rows, err := tx.Query(ctx, `
		SELECT id, payload FROM `+table+`
		WHERE delivered_at IS NULL
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, r.BatchSize)
	if err != nil {
		return 0, err
	}
	type item struct {
		id      int64
		payload []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.payload); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, tx.Commit(ctx)
	}

	for _, it := range items {
		if err := r.Client.Append(ctx, it.payload); err != nil {
			return 0, fmt.Errorf("deliver row %d: %w", it.id, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET delivered_at = now() WHERE id = $1`, it.id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(items), nil
}

// listen holds a dedicated connection LISTENing on the wakeup channel and pushes
// a coalesced signal into wake on each NOTIFY. It reconnects on transient errors
// with a sweep-interval backoff; while it is down the periodic sweep still drains
// the table, so correctness never depends on the listener.
func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		if err := r.listenOnce(ctx, wake); err != nil && ctx.Err() == nil {
			r.Logf("outbox relay: listen: %v (falling back to sweep)", err)
			select {
			case <-ctx.Done():
			case <-time.After(r.SweepInterval):
			}
		}
	}
}

func (r *Relay) listenOnce(ctx context.Context, wake chan<- struct{}) error {
	conn, err := r.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Channel is our constant; Sanitize quotes it defensively all the same.
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{r.Channel}.Sanitize()); err != nil {
		return err
	}
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		select {
		case wake <- struct{}{}:
		default: // a wakeup is already pending; coalesce
		}
	}
}

// HTTPClient delivers evidence to services/audit-service POST /v1/events. The
// bearer is a service-service PASETO token (auth.Signer.IssueService) supplied by
// Token; audit-service gates ingest on the service-service edge (ADR-0045/0046).
type HTTPClient struct {
	BaseURL string // audit-service base, e.g. "http://audit-service:8081"
	HTTP    *http.Client
	Token   func(ctx context.Context) (string, error)
}

func (c *HTTPClient) Append(ctx context.Context, payload []byte) error {
	url := strings.TrimRight(c.BaseURL, "/") + "/v1/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return fmt.Errorf("outbox: audit token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("outbox: audit append status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
