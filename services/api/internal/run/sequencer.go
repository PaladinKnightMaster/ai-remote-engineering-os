package run

import (
	"context"
	"fmt"
	"log"
	"time"
)

// sequencerStore is the slice of the Run store the sequencer drives against.
type sequencerStore interface {
	NextClaimable(ctx context.Context, limit int) ([]string, error)
	ClaimRun(ctx context.Context, id string, leaseSecs int) (bool, error)
	RenewClaim(ctx context.Context, id string, leaseSecs int) error
	Get(ctx context.Context, id string) (Run, error)
	SetStepResult(ctx context.Context, runID string, index int, status, outcome string) error
	FinishRun(ctx context.Context, id, status string) error
}

var _ sequencerStore = (*Store)(nil)

// Sequencer drives launched Runs to completion (ADR-0061): it claims a pending Run,
// then dispatches its steps in order through the gateway, stop-on-first-failure. One
// goroutine per claimed Run (so a parked step's held call does not block other Runs);
// the status-guarded claim is the single-winner gate. The reclaimable lease +
// crash-resume are T-056; bounded concurrency + the launch route are T-057.
type Sequencer struct {
	store    sequencerStore
	driver   StepDriver
	tick     time.Duration
	batch    int
	leaseSec int
}

// NewSequencer builds the Run sequencer with safe defaults (2s scan, batch 10). The
// claim lease is 20m — comfortably above the step client's 15m HTTP timeout (which is
// itself > the gateway's 10m RunWaitTimeout), so a live driver is never reclaimed
// mid-step; only a crashed driver's lease expires (DUR-2, T-056).
func NewSequencer(store sequencerStore, driver StepDriver) *Sequencer {
	return &Sequencer{store: store, driver: driver, tick: 2 * time.Second, batch: 10, leaseSec: 1200}
}

// Run scans for pending Runs on a tick, claims each, and drives it in its own
// goroutine. Best-effort: an error logs and the loop continues. Runs for the life of
// ctx; an in-flight DriveRun shares ctx, so process shutdown cancels it (the run is
// left for the T-056 reclaim).
func (s *Sequencer) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ids, err := s.store.NextClaimable(ctx, s.batch)
			if err != nil {
				log.Printf("run sequencer: scan: %v", err)
				continue
			}
			for _, id := range ids {
				claimed, err := s.store.ClaimRun(ctx, id, s.leaseSec)
				if err != nil {
					log.Printf("run sequencer: claim %s: %v", id, err)
					continue
				}
				if claimed {
					go s.DriveRun(ctx, id)
				}
			}
		}
	}
}

// DriveRun drives one claimed Run to a terminal status: each pending step in order is
// dispatched to the gateway; a governed refusal (IsError) or an infra error stops the
// Run (failed) — applied steps stay applied (non-atomic, ADR-0061 §6). On success of
// every step the Run is completed. The deterministic per-step idempotency key
// (run:<id>:step:<index>) makes a re-issue of an already-run step dedup gateway-side
// (PERF-2), so a future resume never double-execs.
func (s *Sequencer) DriveRun(ctx context.Context, runID string) {
	r, err := s.store.Get(ctx, runID)
	if err != nil {
		log.Printf("run sequencer: load %s: %v", runID, err)
		return
	}
	for _, step := range r.Steps {
		switch step.Status {
		case StepCompleted:
			continue // DUR-3 clean resume: already done — skip, never re-exec
		case StepRunning:
			// DUR-4 (api orphan): a prior drive died mid-step. Fail-closed — do NOT
			// re-dispatch (the gateway's deterministic-key dedup would block it anyway);
			// the operator re-launches. Applied steps stay applied (non-atomic).
			_ = s.store.SetStepResult(ctx, runID, step.Index, StepFailed, "orphaned: driver restarted mid-step")
			_ = s.store.FinishRun(ctx, runID, StatusFailed)
			return
		}
		// pending → renew the lease (so a live driver is not reclaimed mid-step),
		// then dispatch.
		if err := s.store.RenewClaim(ctx, runID, s.leaseSec); err != nil {
			log.Printf("run sequencer: renew %s: %v", runID, err) // best-effort; dedup makes a double-drive safe
		}
		_ = s.store.SetStepResult(ctx, runID, step.Index, StepRunning, "")
		res, err := s.driver.RunStep(ctx, StepRequest{
			Subject:        r.Operator,
			ResourceID:     step.ResourceID,
			Command:        step.Command,
			IdempotencyKey: fmt.Sprintf("run:%s:step:%d", runID, step.Index),
		})
		if err != nil { // infra/transport failure → fail-closed, stop
			_ = s.store.SetStepResult(ctx, runID, step.Index, StepFailed, truncate(err.Error()))
			_ = s.store.FinishRun(ctx, runID, StatusFailed)
			return
		}
		if res.IsError { // governance refusal (denied / timeout / concurrency) → stop
			_ = s.store.SetStepResult(ctx, runID, step.Index, StepFailed, truncate(res.Text))
			_ = s.store.FinishRun(ctx, runID, StatusFailed)
			return
		}
		_ = s.store.SetStepResult(ctx, runID, step.Index, StepCompleted, "")
	}
	_ = s.store.FinishRun(ctx, runID, StatusCompleted)
}

// truncate bounds a step's outcome string (the full detail lives in the audit chain +
// the broker's retained stdout).
func truncate(s string) string {
	const max = 256
	if len(s) > max {
		return s[:max]
	}
	return s
}
