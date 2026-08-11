package checkpoint

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

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
	// dereferencing s.Store, proving degraded mode cannot panic or attempt to
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

func TestRunDegradedModeNeverPanics(t *testing.T) {
	// A Sweeper with Key=nil and a Store that would panic if ever touched
	// (WithCheckpointLock is never expected to be called in degraded mode) —
	// Run must tick through the degraded branch only, with no crash, and
	// return promptly when ctx is canceled.
	s := &Sweeper{Store: panicLockStore{}, Key: nil, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.Run(ctx, false)
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
	panic("WithCheckpointLock must never be called while Key is nil (degraded mode)")
}
