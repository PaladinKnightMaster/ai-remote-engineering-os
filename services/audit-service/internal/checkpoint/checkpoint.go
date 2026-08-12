// Package checkpoint implements the bounded, off-write-path sweep that signs
// audit chain heads (ADR-0069 D2). It is deliberately separate from both the
// ingest path (store.Append + chainLockKey) and from the single-writer
// primitive itself (store.WithCheckpointLock, T-124a) — this package owns
// only the cadence POLICY (when to checkpoint) and the read-then-write
// COMPOSITION (LastHead → ChainTip → decide → SignHead → InsertHead, all on
// one locked transaction).
//
// PERF-14 is load-bearing here: nothing in this package ever touches
// chainLockKey or the Append path. The sweep runs on its own goroutine, on
// its own advisory lock (checkpointLockKey), and a losing sweep tick
// (acquired=false) skips its turn silently rather than retrying or blocking.
//
// T-125b adds one more step after a successful checkpoint: anchoring the
// signed head outside Postgres (ADR-0069 D4, internal/anchor). That call
// happens strictly after WithCheckpointLock has returned — see
// anchorIfSigned's doc comment for why the ordering is fixed, not stylistic.
package checkpoint

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"time"

	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/store"
)

// D2's cadence defaults, overridable via AIREOS_AUDIT_CHECKPOINT_EVENTS
// (integer event count) and AIREOS_AUDIT_CHECKPOINT_INTERVAL (a
// time.ParseDuration string, e.g. "60s") — see main.go's env resolution.
const (
	DefaultEvents   int64         = 100
	DefaultInterval time.Duration = 60 * time.Second
)

// pollPeriod is how often Run wakes to RE-EVALUATE the cadence — deliberately
// decoupled from, and normally shorter than, the configured Interval. Ticking
// exactly at Interval would make the "N events" leg of the OR pointless: a
// burst that crosses the events threshold seconds after the last head would
// still wait until the next Interval-spaced tick to be noticed, silently
// degrading the cadence to "T seconds, always" regardless of volume. Capped
// at 5s so a large configured Interval doesn't force a needlessly tight poll
// loop against a bounded sweep; clamped to Interval itself when Interval is
// configured smaller than that (e2e cadence overrides, T-124b report).
const pollPeriod = 5 * time.Second

// ErrNoKey is returned by Checkpoint when no signing key is configured. Since
// T-125a, a Sweeper only ever has Key==nil under the AUDIT_DEV_INSECURE_AUTH=1
// local-dev escape (main.go's serve() is fatal at boot otherwise) — so this
// error path is dev-only, never a real-install condition. Sweep tick failures
// never surface — checkOnce is a private, ctx-driven op.
var ErrNoKey = errors.New("checkpoint: no chain-head signing key configured")

// lockStore narrows *store.Store to exactly the surface this package needs,
// so the acquired=false / no-op paths are unit-testable without a live
// Postgres — the store layer's actual single-writer guarantee is proven live
// by store_integration_test.go's TestWithCheckpointLockSingleWriter (T-124a).
type lockStore interface {
	WithCheckpointLock(ctx context.Context, fn func(store.CheckpointTx) error) (bool, error)
}

// Outcome describes what one checkpoint attempt did — the three outcomes an
// operator (the `checkpoint` subcommand) or the sweep loop can observe.
type Outcome struct {
	// Signed is true when a new head was signed and inserted.
	Signed bool
	// Seq is the chain position signed, valid only when Signed.
	Seq int64
	// Head is the full signed head, valid only when Signed — carried so the
	// caller (checkOnce, below) can anchor it outside Postgres (ADR-0069 D4)
	// AFTER the transaction that inserted it has committed.
	Head chain.Head
	// LockHeld is true when another writer held checkpointLockKey — this
	// attempt did nothing and must be treated as a silent skip, never an
	// error (ADR-0069 D10: single-writer, losing side never retries).
	LockHeld bool
}

// Anchor is the ADR-0069 D4 out-of-Postgres truncation-detector dependency
// (internal/anchor.Writer satisfies this). Narrowed to just Append so this
// package depends on nothing beyond what its one call site needs. A nil
// Anchor means AIREOS_AUDIT_ANCHOR_PATH was not configured — anchoring is
// then disabled entirely (anchorIfSigned no-ops rather than nil-panicking).
type Anchor interface {
	Append(h chain.Head) error
}

// Sweeper holds everything one audit-service instance needs to run the
// cadence sweep. Key is nil only under the AUDIT_DEV_INSECURE_AUTH=1 local-dev
// escape (AIREOS_AUDIT_CHAIN_SIGN_KEY unset; T-125a made this fatal at boot
// otherwise) — signing is skipped, never attempted, never crashes.
type Sweeper struct {
	Store    lockStore
	Key      ed25519.PrivateKey
	Anchor   Anchor // optional; nil disables anchoring (ADR-0069 D4)
	Events   int64
	Interval time.Duration
}

// Checkpoint performs ONE forced checkpoint attempt right now, bypassing the
// cadence gate entirely — the operator `checkpoint` subcommand (D3: export
// never signs, this is the explicit action that does) and serve()'s
// best-effort final checkpoint on graceful shutdown (D2) both call this.
// Returns ErrNoKey immediately, touching neither ctx's deadline nor the
// store, when no key is configured.
func (s *Sweeper) Checkpoint(ctx context.Context) (Outcome, error) {
	if s.Key == nil {
		return Outcome{}, ErrNoKey
	}
	return s.checkOnce(ctx, true)
}

// Run drives the cadence sweep until ctx is canceled — start it as a
// goroutine bound to a context canceled at shutdown (main.go). It never
// blocks ListenAndServe and never touches chainLockKey.
//
// Key==nil only happens under the AUDIT_DEV_INSECURE_AUTH=1 local-dev escape
// (main.go's serve() is fatal at boot on an absent key otherwise, since
// T-125a) — main.go already logs that condition loudly once at boot, so Run
// simply skips its tick silently rather than repeating the warning forever.
func (s *Sweeper) Run(ctx context.Context) {
	tick := pollPeriod
	if s.Interval > 0 && s.Interval < tick {
		tick = s.Interval
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.Key == nil {
				continue // AUDIT_DEV_INSECURE_AUTH=1 escape; boot already warned once
			}
			out, err := s.checkOnce(ctx, false)
			if err != nil {
				// Fail-loud, never fail-closed (D10): a signing failure must
				// not touch the audit write path or crash the service — it
				// is logged and surfaces as head staleness to a verifier.
				log.Printf("audit checkpoint sweep: %v", err)
				continue
			}
			if out.Signed {
				log.Printf("audit checkpoint: signed chain head at seq %d", out.Seq)
			}
			// out.LockHeld: another instance is checkpointing — silent skip,
			// by design (ADR-0069 D10).
		}
	}
}

// checkOnce runs the whole read-then-write composition on ONE locked
// transaction, per WithCheckpointLock's contract: LastHead, ChainTip, decide,
// SignHead, InsertHead all happen on the CheckpointTx the lock hands the
// callback — never against s.Store directly from inside it (T-124a's
// Commander-review fix; see store/heads.go's dbtx doc comment for the exact
// failure this closes).
func (s *Sweeper) checkOnce(ctx context.Context, force bool) (Outcome, error) {
	var out Outcome
	acquired, err := s.Store.WithCheckpointLock(ctx, func(tx store.CheckpointTx) error {
		last, hasLast, err := tx.LastHead(ctx)
		if err != nil {
			return fmt.Errorf("read last head: %w", err)
		}
		tipSeq, tipHash, hasTip, err := tx.ChainTip(ctx)
		if err != nil {
			return fmt.Errorf("read chain tip: %w", err)
		}
		if !hasTip {
			return nil // empty chain: nothing exists to attest yet
		}
		if hasLast && tipSeq <= last.Seq {
			return nil // no new events since the last head: nothing new to sign
		}

		elapsed := s.Interval // treat "no last head" / unparsable timestamp as due
		var eventsSince int64
		if hasLast {
			if t, perr := time.Parse(time.RFC3339Nano, last.Timestamp); perr == nil {
				elapsed = time.Since(t)
			} else {
				log.Printf("audit checkpoint: could not parse last head timestamp %q, treating cadence as due: %v", last.Timestamp, perr)
			}
			n, cerr := tx.EventCountSince(ctx, last.Seq)
			if cerr != nil {
				return fmt.Errorf("count events since last head: %w", cerr)
			}
			eventsSince = n
		}

		if !decide(force, hasLast, elapsed, eventsSince, s.eventsThreshold(), s.intervalThreshold()) {
			return nil
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		h, serr := chain.SignHead(s.Key, tipSeq, tipHash, now)
		if serr != nil {
			return fmt.Errorf("sign head at seq %d: %w", tipSeq, serr)
		}
		if ierr := tx.InsertHead(ctx, h); ierr != nil {
			return fmt.Errorf("insert head at seq %d: %w", tipSeq, ierr)
		}
		out = Outcome{Signed: true, Seq: tipSeq, Head: h}
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	if !acquired {
		return Outcome{LockHeld: true}, nil
	}

	// ADR-0069 §1(a): anchoring happens HERE — strictly after
	// WithCheckpointLock has returned, i.e. after the transaction that
	// inserted the head has already committed AND the checkpoint advisory
	// lock has been released. Two reasons this order is fixed, not a style
	// choice: (i) anchoring before commit would mean a crash between the two
	// writes leaves the anchor holding a head the DB never committed — the
	// TRUNCATION signature — making ordinary restarts look like attacks;
	// (ii) file I/O from inside the callback above would hold
	// checkpointLockKey across a disk write, undoing the very separation
	// T-124a introduced that lock key for (PERF-14: checkpointing must never
	// neighbor ingest).
	s.anchorIfSigned(out)
	return out, nil
}

// anchorIfSigned appends out.Head to the configured Anchor, if any, and only
// when out.Signed — a no-op for LockHeld or nothing-new-to-sign outcomes,
// and a no-op when no anchor is configured (AIREOS_AUDIT_ANCHOR_PATH unset).
//
// Anchor failure is FAIL-LOUD, NOT FAIL-CLOSED (ADR-0069 D10): logged
// prominently, never returned as an error and never panics. A broken anchor
// degrades the truncation detector; it must never take down chain-head
// signing or serving. Repeated failure naturally surfaces as the "anchor
// behind by many" operational alarm at the next startup re-sync (main.go).
//
// Split out as its own function specifically so it is unit-testable
// (checkpoint_test.go) with a synthetic Outcome and a fake Anchor — the
// lock-then-anchor SEQUENCING proven above can only be exercised against a
// real Postgres transaction, because store.WithCheckpointLock's callback
// signature takes the concrete, unexported-field store.CheckpointTx (T-124a)
// which cannot be constructed outside the store package. That live ordering
// proof is checkpoint_e2e_test.go's G1 extension (T-125b); this function is
// what that boundary calls into, and what THIS package's plain unit tests
// can exercise directly without a database.
func (s *Sweeper) anchorIfSigned(out Outcome) {
	if !out.Signed || s.Anchor == nil {
		return
	}
	if err := s.Anchor.Append(out.Head); err != nil {
		log.Printf("audit checkpoint: anchor append FAILED for seq %d (truncation detector degraded, chain-head signing unaffected): %v", out.Seq, err)
	}
}

func (s *Sweeper) eventsThreshold() int64 {
	if s.Events > 0 {
		return s.Events
	}
	return DefaultEvents
}

func (s *Sweeper) intervalThreshold() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultInterval
}

// decide is the pure D2 cadence policy, factored out so it is unit-testable
// with no database (checkpoint_test.go): sign when EITHER eventsSince >=
// events OR elapsed >= interval, whichever comes first. A chain with no head
// yet always qualifies (D9: any point may be the first checkpoint — it
// attests the whole prefix for free). force (the operator `checkpoint`
// subcommand, or the best-effort final checkpoint at shutdown) bypasses the
// cadence and fires whenever the caller has already confirmed there is
// something new to attest (checkOnce's tipSeq > last.Seq guard, above).
func decide(force, hasLast bool, elapsed time.Duration, eventsSince, events int64, interval time.Duration) bool {
	if !hasLast || force {
		return true
	}
	if elapsed >= interval {
		return true
	}
	return eventsSince >= events
}
