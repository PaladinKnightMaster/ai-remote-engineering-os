// indexer.go is the in-process incremental context-index sweep (ADR-0066 §3; v0.10.0
// CTX-3/K6, T-097). It rides the api's existing background-goroutine pattern (the
// memory expiry sweep + the workstream schedule ticker precedent, main.go) — no new
// systemd unit, env, or key (OI-17); the internal/* extraction trigger stays unfired.
package ctxindex

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// indexerStore is the slice of *Store the Indexer drives against (the
// workstream.tickerStore / run.Launcher interface-seam precedent) — kept narrow and
// swappable so Sweep stays independently testable.
type indexerStore interface {
	AllSources(ctx context.Context) ([]Source, error)
	FileStates(ctx context.Context, sourceID string) (map[string]FileState, error)
	ReindexFile(ctx context.Context, sourceID, filePath string, mtime time.Time, size int64, hash string, chunks []Chunk) error
	TouchFile(ctx context.Context, sourceID, filePath string, mtime time.Time, size int64) error
	RemoveFile(ctx context.Context, sourceID, filePath string) error
	MarkSwept(ctx context.Context, sourceID string) error
	MarkErrored(ctx context.Context, sourceID string) error
}

var _ indexerStore = (*Store)(nil)

// DefaultIndexInterval is the sweep's default cadence (CTX-3) when
// AIREOS_CONTEXT_INDEX_SECONDS is unset — the installed-stack posture (not written by
// install.sh; OI-17's zero-required-env rule).
const DefaultIndexInterval = 60 * time.Second

// fileBatch bounds how many changed (re-hashed-and-different) files ONE sweepSource
// pass will reindex before yielding (PERF-11(3)): a single runaway large source can
// never starve the shared goroutine indefinitely — an incomplete pass simply leaves
// last_indexed_at unadvanced (stale, honestly) and the remaining files are picked up
// on the next tick.
const fileBatch = 500

// Indexer is the incremental context-index sweep (ADR-0066 §3). Sweep is
// deliberately usable standalone (the workstream.Ticker.Run/Sweep split): Run is only
// a thin ticker loop wrapped around it.
type Indexer struct {
	store indexerStore
}

// NewIndexer builds the sweep over a *Store.
func NewIndexer(store *Store) *Indexer {
	return &Indexer{store: store}
}

// Run ticks Sweep on interval until ctx is done (the workstream.Ticker.Run shape).
// Best-effort: a Sweep error logs and the loop continues — the next interval retries.
func (ix *Indexer) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultIndexInterval
	}
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if err := ix.Sweep(ctx); err != nil {
				log.Printf("context indexer: sweep: %v", err)
			}
		}
	}
}

// Sweep is one pass over every registered context source (CTX-3/K6). Per source: a
// confined Root is built over its declared root_path — a missing/unreadable root
// marks the source errored and moves on, NEVER crashing the sweep (CTX-5); its files
// are listed (SEC-33-confined: no traversal, no symlink-escape — root.go's guarantee);
// each is compared against its stored context_files state via mtime+size FIRST —
// unchanged files are never re-read or re-hashed (PERF-11(3)); only a changed
// candidate is hashed, and only a genuine content change is re-chunked and written.
// Files no longer present are removed. A source whose pass completes in full is
// marked swept (fresh last_indexed_at, status=active) — the staleness signal.
//
// Sweep itself never returns an error for a per-source problem (logged and skipped,
// exactly like the workstream ticker's per-charter loop) — only an inability to even
// list sources is surfaced to the caller.
func (ix *Indexer) Sweep(ctx context.Context) error {
	sources, err := ix.store.AllSources(ctx)
	if err != nil {
		return fmt.Errorf("list sources: %w", err)
	}
	for _, src := range sources {
		if err := ix.sweepSource(ctx, src); err != nil {
			log.Printf("context indexer: source %s: %v", src.ID, err)
		}
	}
	return nil
}

func (ix *Indexer) sweepSource(ctx context.Context, src Source) error {
	root, err := NewRoot(src.RootPath)
	if err != nil {
		if merr := ix.store.MarkErrored(ctx, src.ID); merr != nil {
			return fmt.Errorf("root %q unreadable (%v) AND mark-errored failed: %w", src.RootPath, err, merr)
		}
		return nil // errored + surfaced (CTX-5) — not a sweep failure
	}

	files, err := root.List()
	if err != nil {
		if merr := ix.store.MarkErrored(ctx, src.ID); merr != nil {
			return fmt.Errorf("list %q failed (%v) AND mark-errored failed: %w", src.RootPath, err, merr)
		}
		return nil
	}

	states, err := ix.store.FileStates(ctx, src.ID)
	if err != nil {
		return fmt.Errorf("load file states: %w", err)
	}

	seen := make(map[string]bool, len(files))
	changed := 0
	incomplete := false
	for _, rel := range files {
		seen[rel] = true
		prev, hadPrev := states[rel]
		didChange, ferr := ix.sweepFile(ctx, root, src.ID, rel, prev, hadPrev)
		if ferr != nil {
			log.Printf("context indexer: source %s file %s: %v", src.ID, rel, ferr)
			continue
		}
		if didChange {
			changed++
			if changed >= fileBatch {
				// Bound the pass (PERF-11(3)): stop here, leave last_indexed_at
				// unadvanced (honestly stale), and let the next tick pick up the
				// rest — a large source can never starve the shared goroutine.
				incomplete = true
				break
			}
		}
	}
	if incomplete {
		return nil
	}

	for path := range states {
		if !seen[path] {
			if err := ix.store.RemoveFile(ctx, src.ID, path); err != nil {
				log.Printf("context indexer: source %s remove vanished %s: %v", src.ID, path, err)
			}
		}
	}

	return ix.store.MarkSwept(ctx, src.ID)
}

// sweepFile applies the mtime-then-hash prefilter (K6) to one file and reports
// whether it triggered a write (a genuine re-chunk) — used only to bound the batch.
func (ix *Indexer) sweepFile(ctx context.Context, root *Root, sourceID, rel string, prev FileState, hadPrev bool) (changed bool, err error) {
	info, err := os.Stat(filepath.Join(root.Path(), rel))
	if err != nil {
		return false, fmt.Errorf("stat: %w", err)
	}
	mtime := info.ModTime()
	size := info.Size()

	if hadPrev && prev.Mtime.Equal(mtime) && prev.Size == size {
		return false, nil // unchanged — never re-hashed or re-read (PERF-11(3))
	}

	content, err := root.Read(rel)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	hash := HashContent(content)

	if hadPrev && prev.Hash == hash {
		// mtime/size moved but the content did not — refresh the tracked state
		// only; chunks are untouched.
		if err := ix.store.TouchFile(ctx, sourceID, rel, mtime, size); err != nil {
			return false, fmt.Errorf("touch: %w", err)
		}
		return false, nil
	}

	chunks := ChunkFile(rel, content)
	if err := ix.store.ReindexFile(ctx, sourceID, rel, mtime, size, hash, chunks); err != nil {
		return false, fmt.Errorf("reindex: %w", err)
	}
	return true, nil
}
