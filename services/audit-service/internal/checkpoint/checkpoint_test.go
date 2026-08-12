package checkpoint

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"

	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/store"
)

// ---- decide: the pure D2 cadence policy — no database needed ----

func TestDecideEventsThresholdMet(t *testing.T) {
	// 100 events since the last head, well under the 60s interval: the
	// events leg of the OR fires first.
	got := decide(false, true, 5*time.Second, 100, 100, 60*time.Second)
	if !got {
		t.Fatal("expected decide=true when eventsSince >= events")
	}
}

func TestDecideTimeThresholdMet(t *testing.T) {
	// Only 3 events since the last head (well under 100), but 61s have
	// elapsed against a 60s interval: the time leg of the OR fires.
	got := decide(false, true, 61*time.Second, 3, 100, 60*time.Second)
	if !got {
		t.Fatal("expected decide=true when elapsed >= interval")
	}
}

func TestDecideNeitherThresholdMet(t *testing.T) {
	got := decide(false, true, 10*time.Second, 5, 100, 60*time.Second)
	if got {
		t.Fatal("expected decide=false when neither the events nor the time threshold is met")
	}
}

func TestDecideFirstEverCheckpointAlwaysFires(t *testing.T) {
	// No head exists yet (hasLast=false): D9 says any point may be the first
	// checkpoint, so it fires unconditionally regardless of elapsed/eventsSince.
	got := decide(false, false, 0, 0, 100, 60*time.Second)
	if !got {
		t.Fatal("expected decide=true when no head has ever been signed (hasLast=false)")
	}
}

func TestDecideForceBypassesCadence(t *testing.T) {
	// force=true is the operator `checkpoint` subcommand / the best-effort
	// final checkpoint at shutdown — neither threshold is close to met, but
	// force must still fire (checkOnce's caller already confirmed there is
	// something new via tipSeq > last.Seq before calling decide).
	got := decide(true, true, 1*time.Second, 1, 100, 60*time.Second)
	if !got {
		t.Fatal("expected decide=true when force=true, regardless of cadence")
	}
}

// ---- Checkpoint: absent key => no signing, no crash, no store access ----

func TestCheckpointNoKeyReturnsErrNoKeyWithoutTouchingStore(t *testing.T) {
	// Store is deliberately nil: Checkpoint must return ErrNoKey before ever
	// dereferencing s.Store, proving a nil key cannot panic or attempt to
	// sign — "absent key -> no signing and no crash" (T-124b brief §6).
	s := &Sweeper{Store: nil, Key: nil}
	out, err := s.Checkpoint(context.Background())
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("expected ErrNoKey, got %v", err)
	}
	if out.Signed || out.LockHeld {
		t.Fatalf("expected a zero Outcome, got %+v", out)
	}
}

// ---- checkOnce: acquired=false => silent skip, never an error ----

// fakeLockStore simulates another writer already holding checkpointLockKey:
// WithCheckpointLock returns acquired=false immediately and NEVER calls fn —
// exactly store.WithCheckpointLock's own documented contract (T-124a). This
// lets the "another writer holds the lock" outcome be proven at the
// checkpoint package level without a live Postgres; the store layer's actual
// non-blocking single-writer guarantee is proven live by
// store_integration_test.go's TestWithCheckpointLockSingleWriter.
type fakeLockStore struct {
	called bool
}

func (f *fakeLockStore) WithCheckpointLock(ctx context.Context, fn func(store.CheckpointTx) error) (bool, error) {
	f.called = true
	return false, nil
}

func TestCheckOnceLockHeldIsSilentSkip(t *testing.T) {
	fake := &fakeLockStore{}
	_, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	s := &Sweeper{Store: fake, Key: sk, Events: DefaultEvents, Interval: DefaultInterval}

	out, err := s.checkOnce(context.Background(), false)
	if err != nil {
		t.Fatalf("expected acquired=false to be a silent skip, got error: %v", err)
	}
	if !out.LockHeld {
		t.Fatalf("expected Outcome.LockHeld=true, got %+v", out)
	}
	if out.Signed {
		t.Fatalf("expected Outcome.Signed=false when the lock was held elsewhere, got %+v", out)
	}
	if !fake.called {
		t.Fatal("expected WithCheckpointLock to have been called")
	}
}

func TestRunNilKeyNeverPanics(t *testing.T) {
	// A Sweeper with Key=nil (the AUDIT_DEV_INSECURE_AUTH=1 local-dev escape,
	// the only way Key is ever nil since T-125a's boot-fatal flip) and a Store
	// that would panic if ever touched (WithCheckpointLock is never expected
	// to be called while Key is nil) — Run must tick through the nil-key skip
	// branch only, with no crash, and return promptly when ctx is canceled.
	s := &Sweeper{Store: panicLockStore{}, Key: nil, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

type panicLockStore struct{}

func (panicLockStore) WithCheckpointLock(ctx context.Context, fn func(store.CheckpointTx) error) (bool, error) {
	panic("WithCheckpointLock must never be called while Key is nil")
}

// ---- T-125b: anchorIfSigned (ADR-0069 D4) ----

// fakeAnchor records every Append call and can be made to fail — the two
// properties these tests need: WAS it called, and does failure propagate.
type fakeAnchor struct {
	mu    sync.Mutex
	calls []chain.Head
	err   error
}

func (f *fakeAnchor) Append(h chain.Head) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, h)
	return f.err
}

func (f *fakeAnchor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestAnchorIfSignedAppendsOnSignedOutcome proves the happy path: a Signed
// outcome's Head is passed through to the configured Anchor unchanged.
func TestAnchorIfSignedAppendsOnSignedOutcome(t *testing.T) {
	fa := &fakeAnchor{}
	s := &Sweeper{Anchor: fa}
	h := chain.Head{Seq: 42, ChainHash: "abc"}

	s.anchorIfSigned(Outcome{Signed: true, Seq: 42, Head: h})

	if fa.callCount() != 1 {
		t.Fatalf("expected exactly 1 Append call, got %d", fa.callCount())
	}
	if fa.calls[0] != h {
		t.Fatalf("expected Append to receive %+v, got %+v", h, fa.calls[0])
	}
}

// TestAnchorIfSignedSkipsWhenNotSigned proves a LockHeld (or otherwise
// unsigned) outcome never reaches the anchor — anchoring only ever follows a
// REAL commit, never a skipped or no-op checkpoint attempt.
func TestAnchorIfSignedSkipsWhenNotSigned(t *testing.T) {
	fa := &fakeAnchor{}
	s := &Sweeper{Anchor: fa}

	s.anchorIfSigned(Outcome{Signed: false, LockHeld: true})

	if fa.callCount() != 0 {
		t.Fatalf("expected Append to never be called for an unsigned outcome, got %d calls", fa.callCount())
	}
}

// TestAnchorIfSignedNoAnchorConfiguredNeverPanics proves a nil Anchor
// (AIREOS_AUDIT_ANCHOR_PATH unset) is a plain no-op, never a nil-pointer
// panic — anchoring must be optional, not load-bearing for signing itself.
func TestAnchorIfSignedNoAnchorConfiguredNeverPanics(t *testing.T) {
	s := &Sweeper{Anchor: nil}
	s.anchorIfSigned(Outcome{Signed: true, Seq: 1, Head: chain.Head{Seq: 1}})
}

// TestAnchorIfSignedFailureDoesNotPropagate is the ADR-0069 D10 fail-loud
// NOT fail-closed proof: an Append error is swallowed (logged, not
// returned/panicked) — a broken anchor must never take down chain-head
// signing.
func TestAnchorIfSignedFailureDoesNotPropagate(t *testing.T) {
	fa := &fakeAnchor{err: errors.New("disk full")}
	s := &Sweeper{Anchor: fa}

	// anchorIfSigned returns nothing; simply completing without panicking is
	// the assertion. The attempt must still have been MADE, though — a
	// failure must be visible (via the log line), not silently skipped.
	s.anchorIfSigned(Outcome{Signed: true, Seq: 7, Head: chain.Head{Seq: 7}})

	if fa.callCount() != 1 {
		t.Fatalf("expected Append to have been attempted exactly once despite the eventual failure, got %d calls", fa.callCount())
	}
}

// TestCheckOnceLockHeldNeverAnchors extends the existing
// TestCheckOnceLockHeldIsSilentSkip case: when the checkpoint lock is held
// by another writer, checkOnce's real call path must also never reach the
// anchor — proven end-to-end through checkOnce itself, not just through
// anchorIfSigned in isolation.
func TestCheckOnceLockHeldNeverAnchors(t *testing.T) {
	fake := &fakeLockStore{}
	fa := &fakeAnchor{}
	_, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	s := &Sweeper{Store: fake, Key: sk, Anchor: fa, Events: DefaultEvents, Interval: DefaultInterval}

	out, err := s.checkOnce(context.Background(), false)
	if err != nil {
		t.Fatalf("expected acquired=false to be a silent skip, got error: %v", err)
	}
	if !out.LockHeld {
		t.Fatalf("expected Outcome.LockHeld=true, got %+v", out)
	}
	if fa.callCount() != 0 {
		t.Fatalf("expected the anchor to never be called when the checkpoint lock was held elsewhere, got %d calls", fa.callCount())
	}
}
