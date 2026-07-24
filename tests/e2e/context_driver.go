//go:build e2e || wd1

// Shared Layer 1 Live/Context Index driver helpers (ADR-0066) — used by the compose
// e2e WD-10 capstone (wd10_test.go / wd10_negatives_test.go, tag e2e) and, at T-102,
// the installed-stack WD-10 driver (tag wd1). The operator surface
// (/v1/context-sources*) is HTTP-only (no DB), the workstream_driver.go/memory_driver.go
// pattern; the agent-plane context.search tool is driven over the SAME real MCP
// transport the wd9_driver.go helpers use — a real external MCP client, not an
// internal-route shortcut (G1).
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// contextSourceView mirrors services/api/internal/server's contextSourceView (the
// ContextSource wire shape, ADR-0066 §1, T-100). PermissionScope never rides the wire
// (implicit — every route is owner-scoped server-side, the memoryView precedent).
type contextSourceView struct {
	ID            string     `json:"id"`
	RootPath      string     `json:"rootPath"`
	ScopeType     string     `json:"scopeType"`
	ScopeID       string     `json:"scopeId"`
	Status        string     `json:"status"`
	LastIndexedAt *time.Time `json:"lastIndexedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// contextChunkView mirrors services/mcp-gateway/internal/mcp.ContextChunkView
// field-for-field (ADR-0066 §4, T-099) — the content-bearing agent-plane
// context.search result row (the composer needs the text + its provenance to ground
// a plan, unlike the content-free audit event the read chains).
type contextChunkView struct {
	ChunkID  string `json:"chunkId"`
	SourceID string `json:"sourceId"`
	FilePath string `json:"filePath"`
	Locator  string `json:"locator"`
	Content  string `json:"content"`
}

// registerContextSource POSTs a charter-bound context source (operator,
// POST /v1/context-sources, ADR-0066 §1/T-100 — human-agent + CSRF). Asserts 201.
func registerContextSource(t *testing.T, client *http.Client, apiURL, session, csrf, rootPath, workstreamID string) contextSourceView {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"rootPath": rootPath, "workstreamId": workstreamID})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/context-sources", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("register context source: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register context source: status %d, want 201", resp.StatusCode)
	}
	var v contextSourceView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode context source: %v", err)
	}
	return v
}

// listContextSources GETs the caller's owner-scoped context sources
// (GET /v1/context-sources[?workstreamId=], ADR-0066 §1/T-100) — the operator
// source-management view (FE-26); the staleness signal IS lastIndexedAt itself
// (CTX-3, there is no separate boolean flag).
func listContextSources(t *testing.T, client *http.Client, apiURL, session, csrf, workstreamID string) []contextSourceView {
	t.Helper()
	u := apiURL + "/v1/context-sources"
	if workstreamID != "" {
		u += "?" + url.Values{"workstreamId": {workstreamID}}.Encode()
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("list context sources: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list context sources: status %d, want 200", resp.StatusCode)
	}
	var out []contextSourceView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode context sources: %v", err)
	}
	return out
}

// getContextSource finds one context source by id from the owner-scoped list — there
// is no single-GET route (list + register + remove only, T-100), so a caller that
// needs one row's current view filters the list itself.
func getContextSource(t *testing.T, client *http.Client, apiURL, session, csrf, workstreamID, id string) (contextSourceView, bool) {
	t.Helper()
	for _, src := range listContextSources(t, client, apiURL, session, csrf, workstreamID) {
		if src.ID == id {
			return src, true
		}
	}
	return contextSourceView{}, false
}

// waitForSourceIndexed polls listContextSources until the source's lastIndexedAt is
// set (the first sweep completed, CTX-3) — never sleep a fixed guess; the T-097
// mtime-then-hash sweep rides AIREOS_CONTEXT_INDEX_SECONDS in the e2e stack.
func waitForSourceIndexed(t *testing.T, client *http.Client, apiURL, session, csrf, workstreamID, id string, within time.Duration) contextSourceView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if src, ok := getContextSource(t, client, apiURL, session, csrf, workstreamID, id); ok && src.LastIndexedAt != nil {
			return src
		}
		if time.Now().After(deadline) {
			t.Fatalf("context source %s never indexed (lastIndexedAt still nil) within %s", id, within)
		}
		time.Sleep(1 * time.Second)
	}
}

// searchContextMCP invokes the context.search control-plane tool (ADR-0066 §4,
// T-099). Like memory.read/workstream.poll it has NO refusal shape for an
// out-of-grant/foreign workstreamId filter or a query that matches nothing (SEC-33 —
// cross-grant/foreign-source containment answers empty, never an error, N1); isError
// is asserted false unconditionally. workstreamID == "" omits the narrowing filter
// (search everything the caller's grants entitle it to, the memory.read convention).
func searchContextMCP(t *testing.T, client *http.Client, mcpURL, tok, query, workstreamID string) []contextChunkView {
	t.Helper()
	args := map[string]any{"query": query}
	if workstreamID != "" {
		args["workstreamId"] = workstreamID
	}
	text, isErr := callMCPTool(t, client, mcpURL, tok, "context.search", args)
	if isErr {
		t.Fatalf("context.search returned a tool error (want none — no refusal shape): %q", text)
	}
	var out struct {
		Chunks []contextChunkView `json:"chunks"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode context.search result %q: %v", text, err)
	}
	return out.Chunks
}

// waitForSearchHit polls context.search(query, workstreamID) until it returns a chunk
// whose Content contains want, returning the full chunk slice from that successful
// call — the freshness/indexing-completion primitive (WD-10 legs 2/8, N3): never
// sleep a fixed guess, poll until the incremental sweep has actually indexed the
// term.
func waitForSearchHit(t *testing.T, client *http.Client, mcpURL, tok, query, workstreamID, want string, within time.Duration) []contextChunkView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		chunks := searchContextMCP(t, client, mcpURL, tok, query, workstreamID)
		for _, c := range chunks {
			if strings.Contains(c.Content, want) {
				return chunks
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("context.search(%q) never returned a chunk containing %q within %s", query, want, within)
		}
		time.Sleep(1 * time.Second)
	}
}

// wd10ScratchDir creates a real directory under the SAME bind-mounted repo tree every
// e2e Go service container mounts at its working directory (docker-compose.e2e.yml's
// shared x-go anchor, `../../:/work`) — so a file the tester container writes here is
// the EXACT file api's in-process indexer (T-097) reads from its own mount; no new
// shared volume is wired (OI-17: zero new install wiring, the same host directory is
// simply mounted twice). Removed in t.Cleanup — never left behind in the working tree
// (also .gitignore'd as a second line of defense against a failed run).
func wd10ScratchDir(t *testing.T) string {
	t.Helper()
	base, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(base, ".wd10-scratch", wd10Token())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir wd10 scratch dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// writeContextDoc writes (or overwrites) one Markdown doc into dir (K2: docs/tickets
// are chunked by heading, chunker.go) carrying term in its body — the registered
// source's indexable content. A distinct H1 heading + one body line keeps ChunkFile's
// non-empty single-chunk path deterministic and simple.
func writeContextDoc(t *testing.T, dir, name, term string) {
	t.Helper()
	content := "# WD-10 fixture\n\nThe distinctive term is " + term + ".\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write context doc %s: %v", name, err)
	}
}

// wd10TokenSeq is a process-wide counter guaranteeing wd10Token() never repeats even
// across calls landing in the same nanosecond (stdlib-only — no new dependency, R3).
var wd10TokenSeq int64

// wd10Token returns a lowercase-alnum-only unique term (no punctuation — a single,
// cleanly tokenizable tsvector lexeme; native Postgres FTS is `to_tsvector('english',
// ...)`, migration 0014). Distinct from w9suffix (which embeds dots unsuitable for a
// plainto_tsquery search term).
func wd10Token() string {
	n := atomic.AddInt64(&wd10TokenSeq, 1)
	return "wd10x" + strconv.FormatInt(time.Now().UnixNano(), 36) + "x" + strconv.FormatInt(n, 36)
}

// wd10AssertStaleThenClears proves the honest-staleness contract (N3, ADR-0066 §3):
// overwriting dir/fileName with a fresh term leaves the source's lastIndexedAt
// UNCHANGED and the fresh term un-searchable IMMEDIATELY after the edit (surfaced
// honestly to both the operator's source view and the composer's read) — then the
// next sweep clears both (lastIndexedAt advances, the fresh term becomes
// searchable). Self-healing against the rare race where a real sweep completes in the
// few-millisecond window between the pre-edit read and the edit itself: when that
// happens the freshly-observed lastIndexedAt simply becomes the new safe baseline and
// the attempt retries (bounded) — the retry is against a genuine timing coincidence,
// never against the invariant itself.
func wd10AssertStaleThenClears(t *testing.T, client *http.Client, apiURL, mcpURL, session, csrf, tok, workstreamID, sourceID, dir, fileName string, sweepWithin time.Duration) {
	t.Helper()
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		before, ok := getContextSource(t, client, apiURL, session, csrf, workstreamID, sourceID)
		if !ok || before.LastIndexedAt == nil {
			t.Fatalf("source %s missing or never indexed ahead of the staleness leg", sourceID)
		}

		term := wd10Token()
		writeContextDoc(t, dir, fileName, term)

		after, ok := getContextSource(t, client, apiURL, session, csrf, workstreamID, sourceID)
		if !ok {
			t.Fatalf("source %s vanished mid-staleness-check", sourceID)
		}
		hits := searchContextMCP(t, client, mcpURL, tok, term, workstreamID)

		if !after.LastIndexedAt.Equal(*before.LastIndexedAt) || len(hits) != 0 {
			// A sweep raced into the observation window (rare) — the state is simply
			// fresh again; retry from this new baseline rather than flake the test.
			t.Logf("wd10 staleness check attempt %d: a sweep raced the observation window — retrying from a fresh baseline", attempt)
			continue
		}
		t.Logf("PASS WD-10 N3 (part 1/2) — immediately after the edit, lastIndexedAt stayed at %s and the fresh term was not yet searchable (honest staleness, surfaced to both the operator's source view and the composer's read)", before.LastIndexedAt)

		waitForSearchHit(t, client, mcpURL, tok, term, workstreamID, term, sweepWithin)
		cleared, ok := getContextSource(t, client, apiURL, session, csrf, workstreamID, sourceID)
		if !ok || cleared.LastIndexedAt == nil || !cleared.LastIndexedAt.After(*before.LastIndexedAt) {
			t.Fatalf("after the next sweep, lastIndexedAt = %v, want it strictly after %v", cleared.LastIndexedAt, before.LastIndexedAt)
		}
		if cleared.Status != "active" {
			t.Fatalf("source status after the clearing sweep = %q, want active", cleared.Status)
		}
		t.Logf("PASS WD-10 N3 (part 2/2) — the next sweep cleared the stale signal: lastIndexedAt advanced past %s, status active, the fresh term now searchable", before.LastIndexedAt)
		return
	}
	t.Fatalf("wd10 staleness check: a sweep raced the observation window on every one of %d attempts — could not observe a stable stale state", maxAttempts)
}
