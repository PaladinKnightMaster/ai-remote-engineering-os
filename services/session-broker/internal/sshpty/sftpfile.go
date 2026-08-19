// Bounded SFTP file primitives (ADR-0071 §D4/D5), alongside sshpty's PTY (Dial)
// and exec (Exec) modalities. Same package deliberately: it lets this file call
// the unexported dialClient directly, reusing the pinned-host-key dial exactly as
// it is rather than exporting or refactoring it (that dial is on the credential
// path and is out of scope for this ticket). SFTP is a subsystem opened on the
// dialed client, not a shell — Dial's PTY allocation is never invoked here.
//
// Unlike Exec's stdout/stderr, a file's bytes cannot be safely truncated: a
// truncated file is a corrupt file and a digest that matches nothing. So the
// read and write paths here REFUSE an over-cap transfer instead of capping it.
package sshpty

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pkg/sftp"
)

// Sentinel errors for governed file operations. Each is distinguishable via
// errors.Is so a caller (T-139b's HTTP handler) can map it to a distinct status
// — conflating "denied" with "too big" leaves a caller unable to act on the
// difference.
var (
	ErrFileOverCap  = errors.New("file exceeds the configured size cap")
	ErrFileTimeout  = errors.New("file operation timed out")
	ErrFileNotFound = errors.New("no such file or directory")
	ErrFileDenied   = errors.New("permission denied")
	ErrFileNotADir  = errors.New("path is not a directory")
	ErrFileIsADir   = errors.New("path is a directory")
)

// FileEntry describes one directory entry (ListDir).
type FileEntry struct {
	Name    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
}

// ReadFile fetches path's bytes. maxBytes <= 0 means no cap; timeout <= 0
// defaults to 60s. It REFUSES a file over maxBytes rather than truncating it
// (ADR-0071 §D5): the size is checked via Stat before opening, and enforced
// again while copying so a file that grows between Stat and Read cannot slip
// past the cap.
func ReadFile(cfg Config, path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	var out []byte
	err := withSFTP(cfg, timeout, func(sc *sftp.Client) error {
		info, statErr := sc.Stat(path)
		if statErr != nil {
			return mapSFTPError(statErr)
		}
		if info.IsDir() {
			return ErrFileIsADir
		}
		if maxBytes > 0 && info.Size() > maxBytes {
			return fmt.Errorf("%w: %d bytes exceeds cap of %d", ErrFileOverCap, info.Size(), maxBytes)
		}

		f, openErr := sc.Open(path)
		if openErr != nil {
			return mapSFTPError(openErr)
		}
		defer func() { _ = f.Close() }()

		data, readErr := readCapped(f, maxBytes)
		if readErr != nil {
			return readErr
		}
		out = data
		return nil
	})
	return out, err
}

// readCapped copies r's remaining bytes into memory, refusing rather than
// truncating if there are more than maxBytes (ADR-0071 §D5). maxBytes <= 0
// means no cap. It reads one byte past the cap instead of trusting a prior
// Stat — the belt to Stat's suspenders in ReadFile: a file that grows between
// Stat and Read cannot slip past this check, because it depends only on what r
// actually yields, not on what Stat claimed beforehand.
func readCapped(r io.Reader, maxBytes int64) ([]byte, error) {
	var buf bytes.Buffer
	if maxBytes <= 0 {
		if _, err := io.Copy(&buf, r); err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		return buf.Bytes(), nil
	}
	n, err := io.CopyN(&buf, r, maxBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read: %w", err)
	}
	if n > maxBytes {
		return nil, fmt.Errorf("%w: stream exceeds %d bytes", ErrFileOverCap, maxBytes)
	}
	return buf.Bytes(), nil
}

// WriteFile writes data to path with the given mode. maxBytes <= 0 means no
// cap; timeout <= 0 defaults to 60s. It REFUSES an over-cap payload before
// opening the remote file at all — no partial file is ever created.
func WriteFile(cfg Config, path string, data []byte, mode os.FileMode, maxBytes int64, timeout time.Duration) error {
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return fmt.Errorf("%w: %d bytes exceeds cap of %d", ErrFileOverCap, len(data), maxBytes)
	}
	return withSFTP(cfg, timeout, func(sc *sftp.Client) error {
		// Proactively distinguish "is a directory" from whatever generic
		// failure the remote OS would otherwise raise on OpenFile — that
		// varies by platform and pkg/sftp does not normalize it the way it
		// normalizes not-found/permission-denied. A Stat error here (e.g. the
		// path does not exist yet) is not fatal; OpenFile below handles
		// creation and its own errors.
		if info, statErr := sc.Stat(path); statErr == nil && info.IsDir() {
			return ErrFileIsADir
		}

		f, openErr := sc.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if openErr != nil {
			return mapSFTPError(openErr)
		}
		ok := false
		defer func() {
			_ = f.Close()
			if !ok {
				// Best-effort: never leave a corrupt partial file at path.
				_ = sc.Remove(path)
			}
		}()

		if _, writeErr := f.Write(data); writeErr != nil {
			return fmt.Errorf("write: %w", mapSFTPError(writeErr))
		}
		if chmodErr := f.Chmod(mode); chmodErr != nil {
			return fmt.Errorf("chmod: %w", mapSFTPError(chmodErr))
		}
		ok = true
		return nil
	})
}

// ListDir lists one directory's immediate entries. timeout <= 0 defaults to
// 60s.
func ListDir(cfg Config, path string, timeout time.Duration) ([]FileEntry, error) {
	var out []FileEntry
	err := withSFTP(cfg, timeout, func(sc *sftp.Client) error {
		info, statErr := sc.Stat(path)
		if statErr != nil {
			return mapSFTPError(statErr)
		}
		if !info.IsDir() {
			return ErrFileNotADir
		}
		entries, readErr := sc.ReadDir(path)
		if readErr != nil {
			return mapSFTPError(readErr)
		}
		out = make([]FileEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, FileEntry{
				Name:    e.Name(),
				Size:    e.Size(),
				Mode:    e.Mode(),
				ModTime: e.ModTime(),
				IsDir:   e.IsDir(),
			})
		}
		return nil
	})
	return out, err
}

// withSFTP dials cfg's target (pinned host key + key auth, via the shared
// dialClient), opens an SFTP subsystem session on top, and runs op under a
// per-operation timeout. On timeout, the underlying ssh.Client is closed to
// unblock op's in-flight I/O, and withSFTP waits for op's goroutine to exit
// before returning — mirroring Exec's SIGKILL-then-wait shutdown so no
// goroutine or connection is leaked.
func withSFTP(cfg Config, timeout time.Duration, op func(*sftp.Client) error) error {
	client, err := dialClient(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	to := orDur(timeout, 60*time.Second)
	done := make(chan error, 1)
	go func() {
		sc, sessErr := sftp.NewClient(client)
		if sessErr != nil {
			done <- fmt.Errorf("sftp session: %w", sessErr)
			return
		}
		defer func() { _ = sc.Close() }()
		done <- op(sc)
	}()

	timer := time.NewTimer(to)
	defer timer.Stop()
	select {
	case <-timer.C:
		_ = client.Close() // unblocks the goroutine's blocked SFTP I/O
		<-done             // drain so the goroutine is never leaked
		return fmt.Errorf("%w after %s", ErrFileTimeout, to)
	case opErr := <-done:
		return opErr
	}
}

// mapSFTPError normalizes pkg/sftp's client-side error normalization
// (os.ErrNotExist / os.ErrPermission, per its own normaliseError) into this
// package's typed sentinels, so callers only ever see the sentinels defined
// above rather than reaching into pkg/sftp's error shapes themselves.
func mapSFTPError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %v", ErrFileNotFound, err)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%w: %v", ErrFileDenied, err)
	default:
		return err
	}
}
