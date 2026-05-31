package cast

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The byte-safety contract (the spike's load-bearing finding): '"' and '\' are
// escaped, every control byte (<0x20) and high byte (>=0x80) becomes \u00XX, and
// printable ASCII passes through. Go's string→JSON path would corrupt the high
// bytes to U+FFFD. Asserted two ways: the encoded output is pure ASCII (no raw
// high/control byte leaks), and it is valid JSON that decodes (as code points,
// the way the asciinema player reads \u00XX) back to the original bytes.
func TestEncodeJSONStringIsByteSafe(t *testing.T) {
	in := []byte{0x80, 0xff, 0x07, 'A', '"', '\\', 0x1b, '[', 'K'}
	got := encodeJSONString(in)

	for i := 0; i < len(got); i++ {
		if got[i] >= 0x80 {
			t.Fatalf("encoded output has a non-ASCII byte at index %d: %q", i, got)
		}
	}

	var s string
	if err := json.Unmarshal([]byte(got), &s); err != nil {
		t.Fatalf("encoded value is not valid JSON: %v (%s)", err, got)
	}
	want := string([]rune{0x80, 0xff, 0x07, 'A', '"', '\\', 0x1b, '[', 'K'})
	if s != want {
		t.Fatalf("decoded mismatch:\n got  %q\n want %q", s, want)
	}
}

func TestWriterProducesReplayableCastAndStableHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.cast")
	w, err := NewWriter(path, 80, 24)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if err := w.Record(Output, []byte("hello\r\n")); err != nil {
		t.Fatalf("record output: %v", err)
	}
	if err := w.Record(Input, []byte{0xe2, 0x82}); err != nil { // raw partial-UTF8 bytes
		t.Fatalf("record input: %v", err)
	}
	sum, err := w.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cast: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 { // header + 2 events
		t.Fatalf("want 3 lines (header + 2 events), got %d:\n%s", len(lines), raw)
	}

	var header struct {
		Version int `json:"version"`
		Width   int `json:"width"`
		Height  int `json:"height"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil || header.Version != 2 {
		t.Fatalf("bad header: %v (%s)", err, lines[0])
	}
	if header.Width != 80 || header.Height != 24 {
		t.Fatalf("header dims: %+v", header)
	}

	wantCode := []string{"o", "i"}
	for i, ln := range lines[1:] {
		var ev []json.RawMessage
		if err := json.Unmarshal([]byte(ln), &ev); err != nil || len(ev) != 3 {
			t.Fatalf("event %d not a 3-tuple: %v (%s)", i, err, ln)
		}
		var code string
		_ = json.Unmarshal(ev[1], &code)
		if code != wantCode[i] {
			t.Fatalf("event %d code = %q, want %q", i, code, wantCode[i])
		}
	}

	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != sum {
		t.Fatalf("Close hash %s != sha256(file) %s", sum, hex.EncodeToString(h[:]))
	}
}

func TestNewWriterRefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dup.cast")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := NewWriter(path, 80, 24); err == nil {
		t.Fatal("NewWriter on an existing path must fail (O_EXCL)")
	}
}
