// ticker.go — the schedule ticker (ADR-0063 §5; WS-3, T-073): the third api background
// goroutine (the reaper/sequencer pattern) that fires due charters with NOBODY present
// at launch. A tick only CREATES a Run row — through the SAME shared bounded-launch core
// (run.LaunchBounded, SEC-30a) as the gateway forward — and the existing Run sequencer
// drives it. No cron dependency: the schedule is interval_seconds + a durable
// next_fire_at (DUR-8; an api restart fires on time, not from scratch).
package workstream

import (
	"context"
	"fmt"
	"log"
	"time"

	"aireos.local/services/api/internal/run"
)

// tickerStore is the slice of the workstream store the ticker drives against.
type tickerStore interface {
	DueWorkstreams(ctx context.Context, limit int) ([]DueRef, error)
	ClaimTick(ctx context.Context, id string, fireAt time.Time) (bool, error)
	Get(ctx context.Context, id string) (Workstream, error)
	SelfRetire(ctx context.Context, id string) (bool, error)
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
func (t *Ticker) Run(ctx context.Context) {
	tk := time.NewTicker(t.tick)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
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

// fire instantiates the claimed tick's template and launches it through the shared core,
// then maps the typed refusal (the T-072 kinds — no string matching):
//
//   - RefusalBounds  → log only; the core already chained run.launch.denied, and the
//     claim already advanced next_fire_at, so the charter re-fires next interval — a
//     bounds-refused tick never wedges and never retires (WS-3).
//   - RefusalGrantDead → SELF-RETIRE (F1): the grant is revoked or expired; without this
//     the charter would spin denials every interval forever.
//   - RefusalOutOfGrant → should be impossible (the template was validated ⊆ scope at
//     charter time, both immutable) — log loudly, no retire (audited by the core).
//   - infra error → log; re-fires next interval.
func (t *Ticker) fire(ctx context.Context, d DueRef) {
	ws, err := t.store.Get(ctx, d.ID)
	if err != nil {
		log.Printf("workstream ticker: load %s: %v", d.ID, err)
		return
	}
	steps := make([]run.StepInput, 0, len(ws.Steps))
	for _, st := range ws.Steps {
		steps = append(steps, run.StepInput{ResourceID: st.ResourceID, Command: st.Command})
	}
	// Deterministic per intended tick (the claimed fire time): a same-tick retry
	// re-attaches via the (grant_id, launch_key) dedup instead of double-launching.
	key := fmt.Sprintf("workstream:%s:tick:%d", ws.ID, d.NextFireAt.Unix())
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
