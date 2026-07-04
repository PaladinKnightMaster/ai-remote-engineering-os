//go:build e2e

// Pre-release hardening sweep for the v0.6 delegated-launch plane (T-070 rider) —
// the edge + stress scenarios the WD-6 gate (wd6_test.go) deliberately keeps out of
// the single load-bearing walkthrough. Everything here runs LIVE on the compose
// stack; the suite's trailing `audit-service verify` proves the chain stays intact
// under the abuse.
//
// TestDelegatedLaunchEdgeCases — every SEC-29 denial reason live (unknown grant /
// wrong agent / EXPIRED grant / oversized plan), malformed + empty run.launch args
// (the gateway rejects before any forward), the delegated flagged-DENY fail-closed
// abort (run.aborted actor = the agent; no later dispatch), and cross-run step-
// budget exhaustion (consumption counts completed Runs' planned steps).
//
// TestDelegatedLaunchStressAndIdempotency — launch-retry re-attach via the
// Idempotency-Key header (sequential + 5-way concurrent same-key: exactly ONE Run),
// a deterministic per-grant concurrency-cap hit (max_concurrent_runs=1 held by a
// parked step), and an 8-way concurrent distinct-key burst under the cap (every
// response is a clean launch or a clean 429-refusal; every launched Run drains to
// completed; no engine wedge, no duplicate Runs).
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// issueGrantBounded issues a grant with explicit bounds (the shared issueGrant
// hardcodes maxSteps=10/maxConcurrent=4; the edge legs need tighter ones).
func issueGrantBounded(t *testing.T, client *http.Client, apiURL, session, csrf, agent string, resources []string, maxSteps, maxConcurrent, stepBudget int) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"agent": agent, "label": "e2e-edge", "resources": resources, "verbs": []string{"exec"},
		"ttlSeconds": 3600, "maxStepsPerRun": maxSteps, "maxConcurrentRuns": maxConcurrent, "stepBudget": stepBudget,
	})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/grants", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("issue bounded grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issue bounded grant: status %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" {
		t.Fatalf("decode bounded grant: %v", err)
	}
	return out.ID
}

// callRunLaunchArgs posts a run.launch tools/call with RAW argument JSON and an
// optional Idempotency-Key header, single-shot with a short transport retry (the
// stack is warm by the time this suite runs). Returns the tool text + isError.
func callRunLaunchArgs(t *testing.T, client *http.Client, mcpURL, tok, idemKey, argsJSON string) (string, bool) {
	t.Helper()
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run.launch","arguments":` + argsJSON + `}}`
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, mcpURL+"/mcp", strings.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		if idemKey != "" {
			req.Header.Set("Idempotency-Key", idemKey)
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var rpc struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&rpc)
			_ = resp.Body.Close()
			if rpc.Error == nil && len(rpc.Result) > 0 {
				var res struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				}
				if json.Unmarshal(rpc.Result, &res) == nil && len(res.Content) > 0 {
					return res.Content[0].Text, res.IsError
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("run.launch (edge) never returned a result (last err=%v)", err)
		}
		time.Sleep(2 * time.Second)
	}
}

// launchArgs marshals a well-formed run.launch argument object.
func launchArgs(t *testing.T, grantID string, steps []map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"grantId": grantID, "steps": steps})
	if err != nil {
		t.Fatalf("marshal launch args: %v", err)
	}
	return string(b)
}

// runIDFromLaunch parses "launched run <id> (<status>)".
func runIDFromLaunch(t *testing.T, text string) string {
	t.Helper()
	fields := strings.Fields(text)
	if len(fields) < 3 || fields[0] != "launched" {
		t.Fatalf("unexpected run.launch result %q", text)
	}
	return fields[2]
}

func TestDelegatedLaunchEdgeCases(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	pool := runEngineDB(t)
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)
	const agentSubject = "agent:e2e"
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, "e2e", delegatedResourceID)
	oneStep := []map[string]string{{"resourceId": delegatedResourceID, "command": "echo EDGE"}}

	// --- SEC-29 denial reasons, live ------------------------------------------

	// Unknown grant id → refused before anything exists (no Run, no egress).
	text, isErr := callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, "01900000-dead-7000-8000-000000000000", oneStep))
	if !isErr || !strings.Contains(strings.ToLower(text), "no such grant") {
		t.Fatalf("unknown grant = {err:%v %q}, want a no-such-grant refusal", isErr, text)
	}

	// A grant issued to a DIFFERENT agent → the gateway-vouched subject must match.
	otherGrant := issueGrant(t, client, apiURL, session, csrf, "agent:someone-else", []string{delegatedResourceID}, 50)
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, otherGrant, oneStep))
	if !isErr || !strings.Contains(strings.ToLower(text), "does not authorize this agent") {
		t.Fatalf("foreign grant = {err:%v %q}, want an agent-mismatch refusal", isErr, text)
	}

	// EXPIRED grant → refused (the live leg of N1's expired variant; expiry is
	// computed, no sweeper — seeded via DB like the T-056 crash states).
	expiredGrant := issueGrant(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 50)
	if _, err := pool.Exec(context.Background(),
		`UPDATE delegation_grants SET expires_at = now() - interval '1 second' WHERE id = $1`, expiredGrant); err != nil {
		t.Fatalf("age grant: %v", err)
	}
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, expiredGrant, oneStep))
	if !isErr || !strings.Contains(strings.ToLower(text), "expired") {
		t.Fatalf("expired grant = {err:%v %q}, want an expired refusal", isErr, text)
	}
	// The denial is audited (N1 contract: denied + audited, no egress).
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+expiredGrant, "run.launch.denied", 30*time.Second)
	t.Log("PASS SEC-29 denials — unknown grant / foreign agent / expired grant all refused; denial audited")

	// Oversized plan (maxStepsPerRun=2, 3 steps) → refused before any Run exists.
	tightGrant := issueGrantBounded(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 2, 4, 50)
	big := []map[string]string{}
	for i := 0; i < 3; i++ {
		big = append(big, map[string]string{"resourceId": delegatedResourceID, "command": fmt.Sprintf("echo BIG-%d", i)})
	}
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, tightGrant, big))
	if !isErr || !strings.Contains(strings.ToLower(text), "maximum steps") {
		t.Fatalf("oversized plan = {err:%v %q}, want a max-steps refusal", isErr, text)
	}
	t.Log("PASS SEC-29 — plan larger than the grant's per-run bound refused")

	// --- Malformed / empty arguments (gateway-side, before any forward) --------
	grantID := issueGrant(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 50)
	for name, args := range map[string]string{
		"steps not an array": `{"grantId":"` + grantID + `","steps":"nope"}`,
		"missing grantId":    `{"steps":[{"resourceId":"` + delegatedResourceID + `","command":"echo X"}]}`,
		"empty plan":         `{"grantId":"` + grantID + `","steps":[]}`,
		"step missing cmd":   `{"grantId":"` + grantID + `","steps":[{"resourceId":"` + delegatedResourceID + `"}]}`,
	} {
		text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", args)
		if !isErr {
			t.Fatalf("%s: accepted (%q), want a tool error", name, text)
		}
	}
	// The gateway is still healthy after garbage: a good launch goes through.
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, grantID,
		[]map[string]string{{"resourceId": delegatedResourceID, "command": "echo EDGE-OK"}}))
	if isErr {
		t.Fatalf("post-garbage launch failed: %q", text)
	}
	waitRunStatusHTTP(t, client, apiURL, session, csrf, runIDFromLaunch(t, text), "completed", 120*time.Second)
	t.Log("PASS malformed-args — every bad shape is a clean tool error; the gateway stays healthy")

	// --- Delegated flagged step DENIED → fail-closed abort, agent-attributed ---
	gGrant := issueGrant(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID, delegatedFlaggedResourceID}, 50)
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, gGrant, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo DENY-1"},
		{"resourceId": delegatedFlaggedResourceID, "command": "echo DENY-2"},
		{"resourceId": delegatedResourceID, "command": "echo DENY-3"},
	}))
	if isErr {
		t.Fatalf("deny-leg launch failed: %q", text)
	}
	denyRun := runIDFromLaunch(t, text)
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, delegatedFlaggedResourceID, 120*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")
	waitRunStatus(t, pool, denyRun, "failed", 120*time.Second)
	aborted := pollTargetOutcome(t, auditURL, auditTok, "run:"+denyRun, "run.aborted", 30*time.Second)
	if aborted.Actor != agentSubject {
		t.Fatalf("run.aborted actor = %q, want %s (delegated attribution)", aborted.Actor, agentSubject)
	}
	v := getRun(t, client, apiURL, session, csrf, denyRun)
	if v.Steps[2].Status != "pending" || v.Steps[2].InvocationID != "" {
		t.Fatalf("post-deny step 2 = %q/%q, want pending with NO invocation id (fail-closed)", v.Steps[2].Status, v.Steps[2].InvocationID)
	}
	t.Log("PASS delegated deny — flagged step denied → run.aborted (actor=agent), no later dispatch")

	// --- Step-budget exhaustion ACROSS Runs (total-planned semantics) ----------
	bGrant := issueGrantBounded(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 10, 4, 2)
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, bGrant, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo BUD-1"},
		{"resourceId": delegatedResourceID, "command": "echo BUD-2"},
	}))
	if isErr {
		t.Fatalf("budget run launch failed: %q", text)
	}
	waitRunStatusHTTP(t, client, apiURL, session, csrf, runIDFromLaunch(t, text), "completed", 120*time.Second)
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, bGrant,
		[]map[string]string{{"resourceId": delegatedResourceID, "command": "echo BUD-3"}}))
	if !isErr || !strings.Contains(strings.ToLower(text), "budget") {
		t.Fatalf("post-exhaustion launch = {err:%v %q}, want a budget refusal", isErr, text)
	}
	t.Log("PASS budget — a COMPLETED run's planned steps stay consumed; the next launch is refused")
}

func TestDelegatedLaunchStressAndIdempotency(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	const agentSubject = "agent:e2e"
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, "e2e", delegatedResourceID)

	// --- Sequential retry re-attaches (Idempotency-Key header, PERF-2/DEL-2) ---
	grantID := issueGrant(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 50)
	args := launchArgs(t, grantID, []map[string]string{{"resourceId": delegatedResourceID, "command": "echo RETRY"}})
	text, isErr := callRunLaunchArgs(t, client, mcpURL, tok, "stress-retry-1", args)
	if isErr {
		t.Fatalf("keyed launch failed: %q", text)
	}
	first := runIDFromLaunch(t, text)
	waitRunStatusHTTP(t, client, apiURL, session, csrf, first, "completed", 120*time.Second)
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "stress-retry-1", args) // the agent retries
	if isErr || runIDFromLaunch(t, text) != first {
		t.Fatalf("retry = {err:%v %q}, want re-attach to run %s (no double-launch/double-budget)", isErr, text, first)
	}
	t.Logf("PASS idempotent retry — same key re-attached to run %s", first)

	// --- 5-way CONCURRENT same-key launch → exactly ONE Run --------------------
	ids := make(chan string, 5)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &http.Client{Timeout: 60 * time.Second}
			txt, bad := callRunLaunchArgs(t, c, mcpURL, tok, "stress-race-1",
				launchArgs(t, grantID, []map[string]string{{"resourceId": delegatedResourceID, "command": "echo RACE"}}))
			if bad {
				t.Errorf("concurrent same-key launch refused: %q", txt)
				ids <- ""
				return
			}
			ids <- runIDFromLaunch(t, txt)
		}()
	}
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		if id != "" {
			unique[id] = true
		}
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent same-key created %d distinct runs (%v), want exactly 1", len(unique), unique)
	}
	for id := range unique {
		waitRunStatusHTTP(t, client, apiURL, session, csrf, id, "completed", 120*time.Second)
	}
	t.Log("PASS same-key race — 5 concurrent identical launches converged on ONE run")

	// --- Deterministic per-grant concurrency cap (max_concurrent_runs=1) -------
	capGrant := issueGrantBounded(t, client, apiURL, session, csrf, agentSubject,
		[]string{delegatedResourceID, delegatedFlaggedResourceID}, 10, 1, 50)
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, capGrant,
		[]map[string]string{{"resourceId": delegatedFlaggedResourceID, "command": "echo CAP-HOLD"}}))
	if isErr {
		t.Fatalf("cap-holder launch failed: %q", text)
	}
	holder := runIDFromLaunch(t, text)
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, delegatedFlaggedResourceID, 120*time.Second)
	// While the flagged step holds the run ACTIVE, a second launch must be refused.
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, capGrant,
		[]map[string]string{{"resourceId": delegatedResourceID, "command": "echo CAP-2"}}))
	if !isErr || !strings.Contains(strings.ToLower(text), "concurrent") {
		t.Fatalf("over-cap launch = {err:%v %q}, want a concurrent-limit refusal", isErr, text)
	}
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny") // release + fail the holder
	waitRunStatusHTTP(t, client, apiURL, session, csrf, holder, "failed", 60*time.Second)
	// Capacity is back: the same launch now goes through and completes.
	text, isErr = callRunLaunchArgs(t, client, mcpURL, tok, "", launchArgs(t, capGrant,
		[]map[string]string{{"resourceId": delegatedResourceID, "command": "echo CAP-3"}}))
	if isErr {
		t.Fatalf("post-release launch failed: %q", text)
	}
	waitRunStatusHTTP(t, client, apiURL, session, csrf, runIDFromLaunch(t, text), "completed", 120*time.Second)
	t.Log("PASS concurrency cap — a held run blocks the next launch; releasing it restores capacity")

	// --- Concurrent distinct-key burst: two waves of 4 under the aligned caps ---
	// The per-grant concurrency check is a documented NON-ATOMIC soft ceiling
	// (delegated.go), and with actor=agent every in-flight step buckets under ONE
	// gateway PERF-3 subject (cap 4) — the PERF-7 interaction the criteria pin down:
	// the grant bound and the gateway cap must agree. So the deterministic stress
	// shape is waves of exactly 4 (== both caps): every launch must succeed, every
	// run must drain to completed, ids must be unique — 8 runs total on one grant,
	// no wedge, no duplicate, no spurious cap-collision failure.
	burstGrant := issueGrantBounded(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 10, 4, 50)
	launched := map[string]bool{}
	for wave := 0; wave < 2; wave++ {
		results := make(chan string, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				c := &http.Client{Timeout: 60 * time.Second}
				txt, bad := callRunLaunchArgs(t, c, mcpURL, tok, fmt.Sprintf("stress-burst-%d", n),
					launchArgs(t, burstGrant, []map[string]string{{"resourceId": delegatedResourceID, "command": fmt.Sprintf("echo BURST-%d", n)}}))
				if bad {
					t.Errorf("burst launch %d refused under the aligned caps: %q", n, txt)
					results <- ""
					return
				}
				fields := strings.Fields(txt)
				if len(fields) < 3 || fields[0] != "launched" {
					t.Errorf("burst launch %d: unexpected result %q", n, txt)
					results <- ""
					return
				}
				results <- fields[2]
			}(wave*4 + i)
		}
		wg.Wait()
		close(results)
		waveIDs := []string{}
		for id := range results {
			if id == "" {
				continue
			}
			if launched[id] {
				t.Fatalf("burst produced a DUPLICATE run id %s across distinct keys", id)
			}
			launched[id] = true
			waveIDs = append(waveIDs, id)
		}
		// Drain the wave fully before the next (keeps concurrent steps ≤ the caps).
		for _, id := range waveIDs {
			waitRunStatusHTTP(t, client, apiURL, session, csrf, id, "completed", 180*time.Second)
		}
	}
	if len(launched) != 8 {
		t.Fatalf("burst completed %d runs, want 8", len(launched))
	}
	t.Log("PASS burst — 2 waves of 4 concurrent distinct launches: all 8 runs completed, ids unique, no wedge (chain verified by the suite's trailing audit-service verify)")
}
