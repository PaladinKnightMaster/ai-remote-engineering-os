// Package ctxindex is services/api's Layer 1 Live/Context Index store — the fifth
// sibling seam after internal/run, internal/grant, internal/workstream, and
// internal/memory (ADR-0066; v0.10.0 "Live/Context Index"). Named ctxindex rather than
// the seam's natural word ("context") because that collides with the stdlib `context`
// package every file here also imports — a recognized Go anti-pattern (T-097). ctxindex
// is the deterministic, offline, read-only projection of an operator-registered project
// workspace (docs/runbooks/repo/tickets) that a charter's composer reads at plan time
// — the sibling read plane beside Layer 2 (Persistent Scoped Memory, ADR-0064): the
// composer already reads accrued memory; this seam gives it a fresh view of the
// operator's current project context, both grant-scoped and both per-read chained.
//
// A context source is an operator-registered local/mounted directory, bound to exactly
// one charter (`workstream:<id>` — the only v0.10 scope, ADR-0066 §1/K1). Its content is
// chunked (by document/heading for docs+tickets, by file/region for code, T-097) and
// indexed as native Postgres tsvector (§2/R3 — zero embeddings, zero new dependency).
// The agent read plane (SearchForAgent, T-098) reuses the memory package's
// ReadForAgent CTE skeleton: live grants → the grant's charter → the sources bound to
// that charter → FTS over their chunks — cross-grant/foreign-source reads are
// impossible by construction (SEC-33).
//
// T-096 landed the schema (migration 0014) + this store's data-op skeleton
// (CreateSource/GetSource/ListSources/RemoveSource/ReplaceSourceChunks) + the
// confined-root file reader (root.go, SEC-33/K5). T-097 adds the per-file incremental
// state (migration 0015, context_files), the deterministic chunker (chunker.go, K2),
// and the in-process incremental indexer (indexer.go, CTX-3/K6) that rides the api's
// existing background goroutine. No outbox/events here — the operator audit events
// (context.source.registered/removed) land with the routes at T-100; the per-agent-read
// context.read chaining lands with SearchForAgent at T-098. It is deliberately
// self-contained (the internal/run / internal/memory extraction seam precedent):
// nothing outside this package imports it yet.
package ctxindex

import "time"

// Context-source scope types (ADR-0066 §1, K1): charter-bound only at v0.10.
// Operator-shared / run-scoped sources are deferred (their own future scope values).
const ScopeWorkstream = "workstream"

// Context-source status values (CTX-5): active is the normal indexed state; errored
// marks a source whose root is unreadable/absent — surfaced to the operator, never a
// crashed sweep.
const (
	StatusActive  = "active"
	StatusErrored = "errored"
)

// Source is one operator-registered context source (ADR-0066 §1). RootPath is the
// confined-root directory the indexer may read (SEC-33 is enforced in code by
// root.go, not by this row alone). PermissionScope is the owning operator (the
// memories owner-scoping pattern — every read/list/remove is owner-scoped).
// LastIndexedAt is nil until the first sweep completes (ReplaceSourceChunks sets it) —
// its absence/staleness is the source's freshness signal (CTX-3).
type Source struct {
	ID              string
	RootPath        string
	ScopeType       string
	ScopeID         string
	PermissionScope string // the owning operator (owner-scoped reads)
	Status          string
	LastIndexedAt   *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateSourceInput is CreateSource's input. Owner is bound by the server handler from
// the caller's verified capability at T-100 — never accepted from the request body
// (SEC-31/GOV-7 pattern), mirroring memory.CreateInput.Owner.
type CreateSourceInput struct {
	Owner     string
	RootPath  string
	ScopeType string
	ScopeID   string
}

// Chunk is one indexed chunk of a context source's content (ADR-0066 §2). FilePath is
// relative to the source's RootPath — never an absolute host path. Locator names the
// region/heading within the file (K2). FileHash is the source file's content hash
// (T-097's incremental indexer uses it to skip re-chunking unchanged files). Content is
// plaintext, deliberately not sealed — see the migration 0014 comment: native Postgres
// FTS requires indexable text, and the corpus is operator-curated project docs, not
// credentials (SEC-13 untouched).
type Chunk struct {
	ID        string
	SourceID  string
	FilePath  string
	Locator   string
	Content   string
	FileHash  string
	CreatedAt time.Time
}
