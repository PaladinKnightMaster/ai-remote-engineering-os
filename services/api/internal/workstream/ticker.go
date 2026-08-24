// ticker.go — the schedule ticker (ADR-0063 §5; WS-3, T-073): the third api background
// goroutine (the reaper/sequencer pattern) that fires due charters with NOBODY present
// at launch. A tick only CREATES a Run row — through the SAME shared bounded-launch core
// (run.LaunchBounded, SEC-30a) as the gateway forward — and the existing Run sequencer
// drives it. No cron dependency: the schedule is interval_seconds + a durable
// next_fire_at (DUR-8; an api restart fires on time, not from scratch).

package workstream

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/run"
)

// tickerStore is the slice of the workstream store the ticker drives against.
// OpenPlanRequest/ExpirePlanRequestsDue (T-089, PLAN-1/DUR-10) serve the KindAdaptive
// branch and the sweep respectively — the template branch never calls either.
type tickerStore interface {
	DueWorkstreams(ctx context.Context, limit int) ([]DueRef, error)
	ClaimTick(ctx context.Context, id string, fireAt time.Time) (bool, error)
	Get(ctx context.Context, id string) (Workstream, error)
	SelfRetire(ctx context.Context, id string) (bool, error)
	OpenPlanRequest(ctx context.Context, workstreamID, launchKey string, expiresAt time.Time) (PlanRequest, bool, error)
	ExpirePlanRequestsDue(ctx context.Context, batch int) (int, error)
}

var _ tickerStore = (*Store)(nil)

// Launcher is the shared bounded-launch core (SEC-30a) — satisfied by *run.Store. The
// ticker enforces NOTHING itself: plan-vs-grant, the PERF bounds, the windowed budget,
// and the deny-audit all live in the one core both callers share.
type Launcher interface {
	LaunchBounded(ctx context.Context, grants run.GrantSource, agentSubject, grantID, launchKey string, steps []run.StepInput) (run.Run, bool, *run.Refusal, error)
}

// Ticker scans for due charters and fires each claimed tick synchronously — a fire is
// only DB work (the run row insert; the sequencer drives the Run asynchronously), so no
// goroutine-per-fire is needed (unlike the sequencer's held per-Run drives).
type Ticker struct {
	store    tickerStore
	launcher Launcher
	grants   run.GrantSource
	tick     time.Duration
	batch    int
}

// NewTicker builds the schedule ticker with the sequencer's safe defaults (2s scan,
// batch 10).
func NewTicker(store *Store, launcher Launcher, grants run.GrantSource) *Ticker {
	return &Ticker{store: store, launcher: launcher, grants: grants, tick: 2 * time.Second, batch: 10}
}

// Run scans on a tick, CAS-claims each due charter, and fires it. Best-effort: an error
// logs and the loop continues (the charter stays due, or re-fires next interval). Runs
// for the life of ctx.
//
// SWEEP-FIRST (D1/R2, T-089): each pass expires past-due plan requests BEFORE scanning
// for due charters — no new goroutine, no api-reaper coupling, it just rides this
// existing loop. The ordering is load-bearing: an adaptive tick's request TTL is set to
// the CLAIMED fire time + the charter's interval (R1, fireAdaptive), so a stale open
// request is GUARANTEED past-due by the moment the charter's next tick is due; sweeping
// it first clears plan_requests_one_open_idx (C2) so the new OpenPlanRequest below it in
// the same pass never collides with its own predecessor.
func (t *Ticker) Run(ctx context.Context) {
	tk := time.NewTicker(t.tick)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if n, err := t.store.ExpirePlanRequestsDue(ctx, t.batch); err != nil {
				log.Printf("workstream ticker: expire plan requests: %v", err)
			} else if n > 0 {
				log.Printf("workstream ticker: expired %d plan request(s)", n)
			}

			due, err := t.store.DueWorkstreams(ctx, t.batch)
			if err != nil {
				log.Printf("workstream ticker: scan: %v", err)
				continue
			}
			for _, d := range due {
				claimed, err := t.store.ClaimTick(ctx, d.ID, d.NextFireAt)
				if err != nil {
					log.Printf("workstream ticker: claim %s: %v", d.ID, err)
					continue
				}
				if claimed {
					t.fire(ctx, d)
				}
			}
		}
	}
}

// fire loads the claimed tick's charter and branches on Kind (D4, ADR-0065 §4/§5,
// T-089): KindTemplate instantiates the immutable plan and launches it through the
// shared core (byte-untouched from before v0.9); KindAdaptive never touches the
// launch core at all — it opens a durable plan request instead (fireAdaptive, PLAN-1).
// Both branches share the SAME deterministic per-tick key (the fire-time-derived
// dedup carrier — a launch_key for the template, a plan_requests.launch_key for the
// adaptive request).
func (t *Ticker) fire(ctx context.Context, d DueRef) {
	ws, err := t.store.Get(ctx, d.ID)
	if err != nil {
		log.Printf("workstream ticker: load %s: %v", d.ID, err)
		return
	}
	// Deterministic per intended tick (the claimed fire time): a same-tick retry
	// re-attaches via the (grant_id, launch_key) dedup (template) or the launch_key
	// UNIQUE constraint (adaptive) instead of double-firing.
	key := fmt.Sprintf("workstream:%s:tick:%d", ws.ID, d.NextFireAt.Unix())

	if ws.Kind == KindAdaptive {
		t.fireAdaptive(ctx, ws, d, key)
		return
	}

	steps := make([]run.StepInput, 0, len(ws.Steps))
	for _, st := range ws.Steps {
		steps = append(steps, run.StepInput{ResourceID: st.ResourceID, Command: st.Command})
	}
	actor := "workstream:" + ws.ID

	launched, reattached, refusal, err := t.launcher.LaunchBounded(ctx, t.grants, actor, ws.GrantID, key, steps)
	switch {
	case err != nil:
		log.Printf("workstream ticker: %s tick %s: launch infra error (re-fires next interval): %v", ws.ID, key, err)
	case refusal != nil && refusal.Kind == run.RefusalGrantDead:
		ok, rerr := t.store.SelfRetire(ctx, ws.ID)
		if rerr != nil {
			log.Printf("workstream ticker: %s: self-retire on dead grant failed (retries next tick): %v", ws.ID, rerr)
			return
		}
		log.Printf("workstream ticker: %s RETIRED (grant dead: %s; already-retired=%v)", ws.ID, refusal.Reason, !ok)
	case refusal != nil && refusal.Kind == run.RefusalBounds:
		log.Printf("workstream ticker: %s tick refused (%s) — re-fires next interval", ws.ID, refusal.Reason)
	case refusal != nil:
		// OutOfGrant on a consent-validated immutable template = an invariant break.
		log.Printf("workstream ticker: %s tick UNEXPECTEDLY out-of-grant (%s) — investigate; not retiring", ws.ID, refusal.Reason)
	case reattached:
		log.Printf("workstream ticker: %s tick %s re-attached to run %s", ws.ID, key, launched.ID)
	default:
		log.Printf("workstream ticker: %s fired run %s (tick %s)", ws.ID, launched.ID, key)
	}
}

// fireAdaptive is the KindAdaptive branch of fire (D4, ADR-0065 §5, T-089): unlike the
// template branch, an adaptive tick never calls the shared launch core — it opens an
// inbound plan request for the named composer to answer instead (PLAN-1). It FIRST
// checks the charter's grant liveness DIRECTLY, via grantDead (the SAME 3-case
// vocabulary run.ApproveProposed uses: grant missing / status != active / expired) —
// necessary because this branch never reaches LaunchBounded's own liveness check.
// Dead ⇒ SelfRetire (F1, the same self-retire the template branch's RefusalGrantDead
// case triggers; SelfRetire itself closes the charter's open request atomically, R3
// point 3 — no separate close-wiring needed here). Alive ⇒ OpenPlanRequest with
// expiresAt = the CLAIMED fire time (d.NextFireAt) + the charter's interval — NOT
// wallclock now+interval (R1): this makes an unanswered request provably past-due by
// the moment the NEXT tick is due, so the sweep (which the Ticker.Run loop always runs
// BEFORE the next DueWorkstreams scan, R2) is guaranteed to have cleared the way before
// this same charter's next OpenPlanRequest call. A duplicate open (a re-claimed tick,
// or a still-open predecessor) idempotently no-ops to the existing open row.
func (t *Ticker) fireAdaptive(ctx context.Context, ws Workstream, d DueRef, key string) {
	dead, reason, err := t.grantDead(ctx, ws.GrantID)
	if err != nil {
		log.Printf("workstream ticker: %s adaptive tick %s: load grant (re-fires next interval): %v", ws.ID, key, err)
		return
	}
	if dead {
		ok, rerr := t.store.SelfRetire(ctx, ws.ID)
		if rerr != nil {
			log.Printf("workstream ticker: %s: self-retire on dead grant failed (retries next tick): %v", ws.ID, rerr)
			return
		}
		log.Printf("workstream ticker: %s RETIRED (grant dead: %s; already-retired=%v)", ws.ID, reason, !ok)
		return
	}

	expiresAt := d.NextFireAt.Add(time.Duration(ws.IntervalSeconds) * time.Second)
	pr, reattached, err := t.store.OpenPlanRequest(ctx, ws.ID, key, expiresAt)
	if err != nil {
		log.Printf("workstream ticker: %s adaptive tick %s: open plan request (re-fires next interval): %v", ws.ID, key, err)
		return
	}
	if reattached {
		log.Printf("workstream ticker: %s adaptive tick %s re-attached to plan request %s", ws.ID, key, pr.ID)
		return
	}
	log.Printf("workstream ticker: %s opened plan request %s (tick %s)", ws.ID, pr.ID, key)
}

// grantDead re-checks an adaptive charter's grant liveness DIRECTLY (D4, R1, T-089) —
// the SAME 3-case vocabulary run.ApproveProposed uses (grant missing / status !=
// active / expired). It exists because fireAdaptive never calls the shared launch
// core (LaunchBounded already performs this check for the template branch) — the F1
// self-retire trigger still needs a liveness check somewhere for the branch that never
// reaches it.
func (t *Ticker) grantDead(ctx context.Context, grantID string) (dead bool, reason string, err error) {
	g, err := t.grants.Get(ctx, grantID)
	switch {
	case errors.Is(err, grant.ErrNotFound):
		return true, "the grant no longer exists", nil
	case err != nil:
		return false, "", err
	case g.Status != grant.StatusActive:
		return true, "the grant is not active", nil
	case !time.Now().Before(g.ExpiresAt):
		return true, "the grant has expired", nil
	}
	return false, "", nil
}
