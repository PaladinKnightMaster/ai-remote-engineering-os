package brokerfile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// entryGoldenJSON MUST stay byte-identical to the broker's own golden literal
// (services/session-broker/internal/sshpty/sftpfile_test.go's
// fileEntryGoldenJSON) — the two cannot share a Go type (different modules,
// internal/ boundary), so this literal string IS the wire contract. This test
// asserts Entry unmarshals it with every field populated non-zero; the
// broker's test asserts FileEntry marshals to exactly this literal. A key
// mismatch on either side would silently unmarshal to zero values here rather
// than error — a test that only checks its own side proves nothing, so
// neither test is meaningful without the other.
const entryGoldenJSON = `{"name":"nginx.conf","size":1234,"mode":420,"modTime":"2024-01-02T03:04:05Z","isDir":false}`

func TestEntryUnmarshalsGoldenJSON(t *testing.T) {
	var e Entry
	if err := json.Unmarshal([]byte(entryGoldenJSON), &e); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantModTime, err := time.Parse(time.RFC3339, "2024-01-02T03:04:05Z")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	// Every field must come out non-zero and exactly as the broker's FileEntry
	// would have produced it — a wire-key mismatch (e.g. "Name" vs "name")
	// would leave the corresponding field at its zero value WITHOUT an
	// unmarshal error, which is exactly the silent-corruption failure mode
	// Finding 2 warns about.
	if e.Name != "nginx.conf" {
		t.Fatalf("Name = %q, want nginx.conf (zero value means the \"name\" key did not match)", e.Name)
	}
	if e.Size != 1234 {
		t.Fatalf("Size = %d, want 1234 (zero value means the \"size\" key did not match)", e.Size)
	}
	if e.Mode != 420 { // 0644 octal
		t.Fatalf("Mode = %d, want 420 (0644; zero value means the \"mode\" key did not match)", e.Mode)
	}
	if !e.ModTime.Equal(wantModTime) {
		t.Fatalf("ModTime = %v, want %v (zero value means the \"modTime\" key did not match)", e.ModTime, wantModTime)
	}
	// IsDir is deliberately false in the fixture (a zero value that IS the
	// correct value), so it cannot be checked by the "non-zero" pattern above;
	// its coverage is TestListReturnsEntries below, which fixtures IsDir=true
	// and asserts it round-trips.
	if e.IsDir {
		t.Fatalf("IsDir = true, want false per the fixture")
	}
}

// TestListReturnsEntries drives Client.List against a fake broker that answers
// exactly the T-139b wire shape (op:"list" -> {"entries":[...]}), asserting the
// decoded Entry (including IsDir=true, uncovered by the golden fixture above)
// round-trips correctly end to end through the real HTTP client, not just the
// bare json.Unmarshal the golden test exercises.
func TestListReturnsEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req fileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("server: decode request: %v", err)
		}
		if req.Op != "list" || req.ResourceID != "r-1" || req.Path != "/etc" {
			t.Fatalf("server got op=%q resourceId=%q path=%q, want list/r-1//etc", req.Op, req.ResourceID, req.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fileResponse{Entries: []Entry{
			{Name: "subdir", Size: 4096, Mode: 0o755, ModTime: time.Unix(1700000000, 0).UTC(), IsDir: true},
			{Name: "file.txt", Size: 42, Mode: 0o644, ModTime: time.Unix(1700000001, 0).UTC(), IsDir: false},
		}})
	}))
	defer srv.Close()

	c := New(srv.URL, nil)
	entries, err := c.List(context.Background(), "inv-1", "r-1", "/etc", "agent:test")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	if entries[0].Name != "subdir" || !entries[0].IsDir {
		t.Fatalf("entries[0] = %+v, want subdir/IsDir=true", entries[0])
	}
	if entries[1].Name != "file.txt" || entries[1].IsDir {
		t.Fatalf("entries[1] = %+v, want file.txt/IsDir=false", entries[1])
	}
}

// TestListSurfacesBrokerRefusalAsError is the PERF-18 client-level guard: when
// the broker refuses an over-cap listing (413, T-139b's ListDir refuse-don't-
// truncate rule), List must return a Go error — NEVER a nil error with an
// empty or partial []Entry, which would be indistinguishable from "this
// directory genuinely has zero entries."
func TestListSurfacesBrokerRefusalAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "directory exceeds the configured entry cap", http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()

	c := New(srv.URL, nil)
	entries, err := c.List(context.Background(), "inv-1", "r-1", "/huge", "agent:test")
	if err == nil {
		t.Fatalf("List against a 413 refusal = (entries=%v, nil error), want a non-nil error", entries)
	}
	if entries != nil {
		t.Fatalf("List against a 413 refusal returned entries=%v, want nil", entries)
	}
}
