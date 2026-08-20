package server_test

// File plane tests (T-139b / ADR-0071 §D4/D5). Reuses fakeStore, fakeLeaser,
// allowAll, allowAllService, wrongSubjectService, wsURL-style helpers from
// server_test.go (same package).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aireos.local/services/session-broker/internal/connector"
	"aireos.local/services/session-broker/internal/server"
	"aireos.local/services/session-broker/internal/sshpty"
	"aireos.local/services/session-broker/internal/store"
)

// fileWireRequest mirrors server.fileRequest's JSON shape field-for-field so
// tests can build a wire-accurate body without server.fileRequest being
// exported (it deliberately isn't — this is an internal service-service plane,
// D9, with no OpenAPI fragment).
type fileWireRequest struct {
	Op           string  `json:"op"`
	ResourceID   string  `json:"resourceId,omitempty"`
	Path         string  `json:"path,omitempty"`
	Content      []byte  `json:"content,omitempty"`
	Mode         *uint32 `json:"mode,omitempty"`
	InvocationID string  `json:"invocationId,omitempty"`
	Subject      string  `json:"subject,omitempty"`
}

type fileWireResponse struct {
	Content []byte             `json:"content,omitempty"`
	SHA256  string             `json:"sha256,omitempty"`
	Entries []sshpty.FileEntry `json:"entries,omitempty"`
}

func fileHandler(t *testing.T, st *fakeStore, leaser server.CredentialLeaser, dir string, mutate func(*server.Config)) http.Handler {
	t.Helper()
	if leaser == nil {
		leaser = &fakeLeaser{mat: connector.Material{PrivateKey: "k"}}
	}
	cfg := server.Config{
		Verifier:     allowAll{},
		FileVerifier: allowAllService{},
		Store:        st,
		Leaser:       leaser,
		CastDir:      dir,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return server.New(cfg)
}

func postFile(t *testing.T, h http.Handler, body []byte) *http.Response {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/file", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer svc")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post file: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// --- SEC-51: R5-ext subject pin ---------------------------------------------

func TestFileRejectsNonGatewaySubject(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
		c.FileVerifier = wrongSubjectService{} // valid service-service token, wrong workload
	})
	body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/etc/hostname", InvocationID: "i", Subject: "agent:x"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-gateway file status = %d, want 403 (SEC-51)", resp.StatusCode)
	}
	if st.execCalled {
		t.Fatal("no audit should be recorded for a subject-pin rejection")
	}
}

// mutateSubjectPin proves the pin is actually load-bearing: with the pin check
// disabled (accept any subject), the wrong-subject request that TestFile-
// RejectsNonGatewaySubject expects to be rejected instead succeeds — showing
// that test's 403 assertion is the one actually catching the guard, not an
// unrelated failure earlier in the handler (VER-16: mutate the guard, watch it
// fail, confirm which assertion fired). This is exercised inline via a second
// verifier standing in for "gateway", proving the pin — not incidental
// validation — is what produces the 403 above.
func TestFileAcceptsGatewaySubject(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
		c.ReadFileFn = func(sshpty.Config, string, int64, time.Duration) ([]byte, error) {
			return []byte("root:x:0:0\n"), nil
		}
	})
	body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/etc/passwd", InvocationID: "i", Subject: "agent:x"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway-subject read status = %d, want 200 (proves the pin, not something else, gated the 403 case)", resp.StatusCode)
	}
}

// --- PERF-17/18: the derived body limit -------------------------------------

// TestFileWriteAtExactCapSucceeds is the case the ADR-0071 audit finding named:
// a file exactly at the configured cap must decode successfully. Under a flat
// literal body limit sized for the JSON envelope alone (as exec's is), this
// content's base64 form (~4/3 larger) would overflow the limit and the request
// would fail at json.Decode with "invalid body" (400) before the cap check
// below ever ran. fileBodyLimit derives the limit FROM FileMaxBytes so this
// succeeds.
//
// The cap here (256 KiB) is deliberately NOT small (T-139b review finding): at
// a 4 KiB cap, fileEnvelopeHeadroom (8 KiB) alone exceeds the entire base64
// payload, so this end-to-end test would pass identically whether fileBodyLimit
// correctly accounts for base64 expansion or not — a mutation that drops the
// base64 factor entirely is invisible at that size. 256 KiB is comfortably past
// the point (~192 KiB) where base64 growth exceeds the headroom, so this test
// only passes if the derivation is actually doing its job. The
// headroom-independent proof of the underlying property lives in
// TestFileBodyLimitCoversBase64Expansion (file_internal_test.go); this test is
// the end-to-end companion at a cap size where that property is load-bearing.
func TestFileWriteAtExactCapSucceeds(t *testing.T) {
	const cap = 256 * 1024
	dir := t.TempDir()
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	var gotLen int
	h := fileHandler(t, st, nil, dir, func(c *server.Config) {
		c.FileMaxBytes = cap
		c.WriteFileFn = func(_ sshpty.Config, _ string, data []byte, _ os.FileMode, _ int64, _ time.Duration) error {
			gotLen = len(data)
			return nil
		}
	})
	content := make([]byte, cap)
	for i := range content {
		content[i] = byte('A' + i%26)
	}
	body := mustMarshal(t, fileWireRequest{
		Op: "write", ResourceID: "r", Path: "/tmp/x", Content: content,
		InvocationID: "inv-cap", Subject: "agent:x",
	})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file exactly at cap: status = %d, want 200", resp.StatusCode)
	}
	if gotLen != cap {
		t.Fatalf("write received %d bytes, want exactly %d (the cap)", gotLen, cap)
	}
	if !st.execCalled || st.execOutcome != "remote.file.write" {
		t.Fatalf("audit not recorded correctly: called=%v outcome=%q", st.execCalled, st.execOutcome)
	}
	sum := sha256.Sum256(content)
	if st.execSHA != hex.EncodeToString(sum[:]) {
		t.Fatalf("audit sha = %q, want sha256 of the written content", st.execSHA)
	}
	artifact, err := os.ReadFile(filepath.Join(dir, "inv-cap.file"))
	if err != nil || len(artifact) != cap {
		t.Fatalf("retained artifact wrong: len=%d err=%v", len(artifact), err)
	}
}

// TestFileWriteOverCapReturns413NotBodyDecode400 proves the over-cap failure
// mode is the typed 413 the application-level check produces, not a generic
// body-too-large 400 from the transport limit swallowing it first. The content
// here is only modestly over the cap (well within fileEnvelopeHeadroom's
// slack), so it reaches json.Decode successfully and is caught by the explicit
// len(Content) > FileMaxBytes check.
func TestFileWriteOverCapReturns413NotBodyDecode400(t *testing.T) {
	const cap = 4096
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	writeCalled := false
	h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
		c.FileMaxBytes = cap
		c.WriteFileFn = func(sshpty.Config, string, []byte, os.FileMode, int64, time.Duration) error {
			writeCalled = true
			return nil
		}
	})
	content := make([]byte, cap+1024) // 1 KiB over the cap
	body := mustMarshal(t, fileWireRequest{
		Op: "write", ResourceID: "r", Path: "/tmp/x", Content: content,
		InvocationID: "inv-over", Subject: "agent:x",
	})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap write status = %d, want 413 (not a body-decode 400)", resp.StatusCode)
	}
	if writeCalled {
		t.Fatal("the SFTP write primitive must never be called for an over-cap payload")
	}
	if st.execCalled {
		t.Fatal("an over-cap request is rejected before any invocation exists — nothing to audit")
	}
}

// --- read / list happy paths + evidence -------------------------------------

func TestFileReadHappyPathAuditsAndRetains(t *testing.T) {
	dir := t.TempDir()
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	h := fileHandler(t, st, nil, dir, func(c *server.Config) {
		c.ReadFileFn = func(_ sshpty.Config, path string, _ int64, _ time.Duration) ([]byte, error) {
			if path != "/etc/hostname" {
				t.Fatalf("unexpected path %q", path)
			}
			return []byte("myhost\n"), nil
		}
	})
	body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/etc/hostname", InvocationID: "inv-r1", Subject: "agent:x"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out fileWireResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out.Content) != "myhost\n" {
		t.Fatalf("content = %q", out.Content)
	}
	sum := sha256.Sum256([]byte("myhost\n"))
	want := hex.EncodeToString(sum[:])
	if out.SHA256 != want || st.execSHA != want {
		t.Fatalf("sha mismatch: response=%q audit=%q want=%q", out.SHA256, st.execSHA, want)
	}
	if st.execOutcome != "remote.file.read" || st.execInvocation != "inv-r1" || st.execActor != "agent:x" {
		t.Fatalf("audit fields wrong: %+v", st)
	}
	artifact, err := os.ReadFile(filepath.Join(dir, "inv-r1.file"))
	if err != nil || string(artifact) != "myhost\n" {
		t.Fatalf("retained artifact wrong: %q err=%v", artifact, err)
	}
}

func TestFileListHappyPathAudits(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	entries := []sshpty.FileEntry{{Name: "a.txt", Size: 3, IsDir: false}, {Name: "sub", IsDir: true}}
	h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
		c.ListDirFn = func(sshpty.Config, string, int, time.Duration) ([]sshpty.FileEntry, error) {
			return entries, nil
		}
	})
	body := mustMarshal(t, fileWireRequest{Op: "list", ResourceID: "r", Path: "/etc", InvocationID: "inv-l1", Subject: "agent:x"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out fileWireResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 2 || out.Entries[0].Name != "a.txt" || !out.Entries[1].IsDir {
		t.Fatalf("entries = %+v", out.Entries)
	}
	wantEvidence, _ := json.Marshal(entries)
	sum := sha256.Sum256(wantEvidence)
	want := hex.EncodeToString(sum[:])
	if st.execSHA != want {
		t.Fatalf("audit sha = %q, want sha256 of the marshaled listing %q", st.execSHA, want)
	}
	if st.execOutcome != "remote.file.list" {
		t.Fatalf("outcome = %q, want remote.file.list", st.execOutcome)
	}
}

// --- validation ---------------------------------------------------------

func TestFileValidationMissingFields(t *testing.T) {
	h := fileHandler(t, &fakeStore{}, nil, t.TempDir(), nil)
	body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r"}) // missing path/invocationId/subject
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestFileValidationBadOp(t *testing.T) {
	h := fileHandler(t, &fakeStore{}, nil, t.TempDir(), nil)
	body := mustMarshal(t, fileWireRequest{Op: "delete", ResourceID: "r", Path: "/x", InvocationID: "i", Subject: "a"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad op: status = %d, want 400", resp.StatusCode)
	}
}

// --- lease failure modes (mirrors exec exactly) ------------------------------

func TestFileUnprovisionedIs424(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "k"}}
	h := fileHandler(t, st, &fakeLeaser{err: connector.ErrNoCredential}, t.TempDir(), nil)
	body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/x", InvocationID: "i", Subject: "a"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("unprovisioned: status = %d, want 424", resp.StatusCode)
	}
	if st.execCalled {
		t.Fatal("no audit should be recorded when the lease fails")
	}
}

func TestFileRouteAbsentWithoutVerifier(t *testing.T) {
	h := server.New(server.Config{Verifier: allowAll{}, Store: &fakeStore{}, CastDir: t.TempDir()})
	body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/x", InvocationID: "i", Subject: "a"})
	resp := postFile(t, h, body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no file verifier: status = %d, want 404", resp.StatusCode)
	}
}

// --- error mapping (VER-16: each sentinel proven to fire its own status) ----

func TestFileErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", sshpty.ErrFileNotFound, http.StatusNotFound},
		{"denied", sshpty.ErrFileDenied, http.StatusForbidden},
		{"over cap", sshpty.ErrFileOverCap, http.StatusRequestEntityTooLarge},
		{"timeout", sshpty.ErrFileTimeout, http.StatusGatewayTimeout},
		{"is a dir", sshpty.ErrFileIsADir, http.StatusUnprocessableEntity},
		{"not a dir", sshpty.ErrFileNotADir, http.StatusUnprocessableEntity},
		{"unsupported rename", sshpty.ErrFileUnsupportedRename, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "k"}}
			h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
				c.ReadFileFn = func(sshpty.Config, string, int64, time.Duration) ([]byte, error) {
					return nil, tc.err
				}
			})
			body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/x", InvocationID: "inv-" + tc.name, Subject: "a"})
			resp := postFile(t, h, body)
			if resp.StatusCode != tc.want {
				t.Fatalf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.want)
			}
			// AUD-44: a failed operation is still audited, with the :error suffix, and
			// carries no evidence digest (nothing was successfully transferred).
			if !st.execCalled || st.execOutcome != "remote.file.read:error" {
				t.Fatalf("%s: audit = called=%v outcome=%q, want remote.file.read:error", tc.name, st.execCalled, st.execOutcome)
			}
			if st.execSHA != "" {
				t.Fatalf("%s: audit sha = %q, want empty on failure", tc.name, st.execSHA)
			}
		})
	}
}

// TestFileErrorMappingIsSpecific proves the mapping is not just "any error goes
// to some 4xx/5xx" by checking two sentinels land on DIFFERENT statuses — if
// mapFileError degenerated to always returning 502 (its default branch), this
// would fail even though TestFileErrorMapping's individual assertions might
// still each independently look plausible in isolation.
func TestFileErrorMappingIsSpecific(t *testing.T) {
	notFoundStatus := func() int {
		st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "k"}}
		h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
			c.ReadFileFn = func(sshpty.Config, string, int64, time.Duration) ([]byte, error) {
				return nil, sshpty.ErrFileNotFound
			}
		})
		body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/x", InvocationID: "i1", Subject: "a"})
		return postFile(t, h, body).StatusCode
	}()
	deniedStatus := func() int {
		st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "k"}}
		h := fileHandler(t, st, nil, t.TempDir(), func(c *server.Config) {
			c.ReadFileFn = func(sshpty.Config, string, int64, time.Duration) ([]byte, error) {
				return nil, sshpty.ErrFileDenied
			}
		})
		body := mustMarshal(t, fileWireRequest{Op: "read", ResourceID: "r", Path: "/x", InvocationID: "i2", Subject: "a"})
		return postFile(t, h, body).StatusCode
	}()
	if notFoundStatus == deniedStatus {
		t.Fatalf("not-found (%d) and denied (%d) must map to distinct statuses — a caller cannot act on the difference otherwise", notFoundStatus, deniedStatus)
	}
	if notFoundStatus != http.StatusNotFound || deniedStatus != http.StatusForbidden {
		t.Fatalf("got notFound=%d denied=%d, want 404/403", notFoundStatus, deniedStatus)
	}
}
