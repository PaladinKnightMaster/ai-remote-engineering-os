// Package cast writes asciinema v2 cast files for cockpit sessions (ADR-0048 §6,
// §SEC-11). The recorder captures both the rendered terminal output ("o") and
// the raw operator input ("i"); the cast's SHA256 (returned by Close) is anchored
// in the session.close audit event so the recording is tamper-evident.
//
// Byte-safety is load-bearing (spike finding, docs/spike/xterm-ssh-pty-findings.md
// §"UTF-8 silent corruption"): PTY streams carry raw 8-bit bytes — terminal
// escape sequences, locale-dependent output, and UTF-8 multibyte runes split
// across WS frames. Go's json.Marshal on a string with invalid UTF-8 silently
// substitutes U+FFFD, losing data with no error. The encoder here builds the JSON
// string literal byte-by-byte instead, escaping every control byte and every byte
// >= 0x80 as \u00XX (which the asciinema player reads back as the original byte),
// so a cast round-trips exactly.
package cast

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stream is the asciinema event code: output or input.
type Stream byte

const (
	Output Stream = 'o' // rendered terminal output (what the screen showed)
	Input  Stream = 'i' // raw operator input (what they typed on the wire)
)

// Writer records one session to a cast file while maintaining a streaming SHA256
// (kept in sync with each write, so Close need not re-read an 80MB file). It is
// safe for concurrent Record calls (the output-pump and input-audit goroutines
// both write).
type Writer struct {
	f     *os.File
	start time.Time

	mu     sync.Mutex
	out    io.Writer // MultiWriter(file, hash)
	hash   hash.Hash
	closed bool
}

// NewWriter creates path (O_EXCL: refuses to overwrite a pre-existing file — IDs
// are UUID v7 so a collision is a bug, not a race) and writes the v2 header for a
// cols×rows terminal. The file is mode 0600 (target output may be sensitive).
func NewWriter(path string, cols, rows int) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cast: create %s: %w", path, err)
	}
	h := sha256.New()
	w := &Writer{
		f:     f,
		start: time.Now(),
		out:   io.MultiWriter(f, h),
		hash:  h,
	}
	header := fmt.Sprintf(`{"version":2,"width":%d,"height":%d,"timestamp":%d}`+"\n",
		cols, rows, w.start.Unix())
	if _, err := io.WriteString(w.out, header); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cast: write header: %w", err)
	}
	return w, nil
}

// Record appends one event. Unlike the spike (which ignored write errors), a
// write failure is returned so the caller can decide (log + continue at v0.1.0).
// The elapsed timestamp is mutex-acquire time — acceptable at v0.1.0; lock-free
// timestamping is a documented future improvement.
func (w *Writer) Record(s Stream, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("cast: record after close")
	}
	elapsed := strconv.FormatFloat(time.Since(w.start).Seconds(), 'f', 6, 64)
	line := "[" + elapsed + ", \"" + string(rune(s)) + "\", " + encodeJSONString(data) + "]\n"
	if _, err := io.WriteString(w.out, line); err != nil {
		return fmt.Errorf("cast: record: %w", err)
	}
	return nil
}

// Close flushes and closes the file, returning the hex SHA256 of the complete
// cast (the anchor recorded in the session.close audit event).
func (w *Writer) Close() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return "", fmt.Errorf("cast: already closed")
	}
	w.closed = true
	sum := hex.EncodeToString(w.hash.Sum(nil))
	if err := w.f.Close(); err != nil {
		return sum, fmt.Errorf("cast: close file: %w", err)
	}
	return sum, nil
}

// encodeJSONString builds a JSON string literal from raw bytes without going
// through Go's string→UTF-8 path. Printable ASCII passes through; '"' and '\' are
// escaped; every control byte (<0x20) and every high byte (>=0x80) becomes
// \u00XX, which the asciinema player decodes back to the original byte.
func encodeJSONString(data []byte) string {
	var b strings.Builder
	b.Grow(len(data) + 2)
	b.WriteByte('"')
	for _, c := range data {
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c < 0x20 || c >= 0x80:
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
