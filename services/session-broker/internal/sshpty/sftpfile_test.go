package sshpty

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startSSHServer brings up an in-process SSH server on a random localhost port
// that accepts any client key and dispatches each "session" channel's requests
// to handle. Returns the server address and its host key as an
// authorized_keys line — the same shape sshpty_test.go's startServer uses,
// generalized here so both a real SFTP transport and a deliberately-hanging
// one can be plugged in per test.
func startSSHServer(t *testing.T, handle func(ssh.Channel, <-chan *ssh.Request)) (addr, hostKeyLine string) {
	t.Helper()
	_, serverPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("server key: %v", err)
	}
	serverSigner, err := ssh.NewSignerFromKey(serverPriv)
	if err != nil {
		t.Fatalf("server signer: %v", err)
	}
	hostKeyLine = string(ssh.MarshalAuthorizedKey(serverSigner.PublicKey()))

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(serverSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					_ = c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					if newCh.ChannelType() != "session" {
						_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel")
						continue
					}
					ch, chReqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					go handle(ch, chReqs)
				}
				_ = sc
			}()
		}
	}()
	return ln.Addr().String(), hostKeyLine
}

// sftpSubsystemHandler answers a "subsystem sftp" request by serving a real
// pkg/sftp.Server rooted at rootDir (relative client paths resolve inside it).
// This gives the tests below a genuine SFTP wire-protocol transport — real
// SSH_FXP_* packets, real os.Open/os.Stat/os.ReadDir against rootDir — rather
// than a mock of the protocol.
func sftpSubsystemHandler(rootDir string) func(ssh.Channel, <-chan *ssh.Request) {
	return func(ch ssh.Channel, reqs <-chan *ssh.Request) {
		for req := range reqs {
			ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
			if req.WantReply {
				_ = req.Reply(ok, nil)
			}
			if !ok {
				continue
			}
			srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(rootDir))
			if err != nil {
				return
			}
			_ = srv.Serve()
			_ = srv.Close()
			return
		}
	}
}

// hangingSubsystemHandler accepts a "subsystem sftp" request (so the client's
// dial and channel-open both succeed) but then never writes another byte, so
// a client blocked on the SFTP init handshake hangs until its OWN timeout
// forces the connection closed. Used by TestWithSFTP_TimesOut to prove
// withSFTP's per-operation timeout actually fires and unblocks — a merely
// slow-but-eventually-responding server would not distinguish "the timeout
// fired" from "the operation happened to finish quickly".
func hangingSubsystemHandler(_ ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		ok := req.Type == "subsystem"
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
		// Deliberately serve nothing: the client's sftp.NewClient blocks
		// forever reading the version-negotiation response.
	}
}

func testConfig(t *testing.T, addr, hostKeyLine string) Config {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	return Config{
		Host: host, Port: port, User: "u",
		HostKey: hostKeyLine, PrivateKey: clientKeyPEM(t),
		DialTimeout: 5 * time.Second,
	}
}

// --- ReadFile ---

func TestReadFile_HappyPath(t *testing.T) {
	rootDir := t.TempDir()
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	want := []byte("hello sftp")
	if err := os.WriteFile(filepath.Join(rootDir, "greeting.txt"), want, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	got, err := ReadFile(cfg, "greeting.txt", 1024, 5*time.Second)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	rootDir := t.TempDir()
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	if _, err := ReadFile(cfg, "missing.txt", 1024, 5*time.Second); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("want ErrFileNotFound, got %v", err)
	}
}

func TestReadFile_RefusesDirectory(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootDir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	if _, err := ReadFile(cfg, "adir", 1024, 5*time.Second); !errors.Is(err, ErrFileIsADir) {
		t.Fatalf("want ErrFileIsADir, got %v", err)
	}
}

// TestReadFile_RefusesOverCap is the VER-16 proof for the read-side bound
// (PERF-17/18): a file one byte over the cap must be refused, and refused
// SPECIFICALLY with ErrFileOverCap — not truncated, not some other error. The
// exact-cap case is asserted alongside it so the failure below is
// attributable to the cap itself, not an off-by-one in the boundary.
func TestReadFile_RefusesOverCap(t *testing.T) {
	rootDir := t.TempDir()
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	content := bytes.Repeat([]byte("x"), 100)
	if err := os.WriteFile(filepath.Join(rootDir, "big.bin"), content, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if _, err := ReadFile(cfg, "big.bin", 100, 5*time.Second); err != nil {
		t.Fatalf("read at exact cap must succeed: %v", err)
	}

	got, err := ReadFile(cfg, "big.bin", 99, 5*time.Second)
	if err == nil {
		t.Fatal("want refusal reading a 100-byte file with a 99-byte cap, got nil error")
	}
	if !errors.Is(err, ErrFileOverCap) {
		t.Fatalf("want ErrFileOverCap, got %v", err)
	}
	if got != nil {
		t.Fatalf("want no partial bytes on refusal, got %d bytes", len(got))
	}
}

// TestReadCapped_EnforcesDuringRead is logic-level (no network): it proves the
// copy-time cap in readCapped fires on its own, independent of ReadFile's
// Stat-based pre-check — the belt-and-suspenders property ADR-0071 §D5
// requires ("enforce while reading" must not rely on Stat alone). A stream
// that simply yields more bytes than the cap, with no Stat involved at all,
// still gets refused.
func TestReadCapped_EnforcesDuringRead(t *testing.T) {
	over := bytes.NewReader(bytes.Repeat([]byte("y"), 10))
	if got, err := readCapped(over, 9); !errors.Is(err, ErrFileOverCap) || got != nil {
		t.Fatalf("want (nil, ErrFileOverCap) for a 10-byte stream capped at 9, got (%v, %v)", got, err)
	}

	exact := bytes.NewReader(bytes.Repeat([]byte("y"), 9))
	got, err := readCapped(exact, 9)
	if err != nil {
		t.Fatalf("exact cap: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("want 9 bytes, got %d", len(got))
	}
}

// --- WriteFile ---

func TestWriteFile_HappyPath(t *testing.T) {
	rootDir := t.TempDir()
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	data := []byte("written by the broker")
	if err := WriteFile(cfg, "out.txt", data, 0o640, 1024, 5*time.Second); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(rootDir, "out.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
	info, err := os.Stat(filepath.Join(rootDir, "out.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("got mode %v, want 0640", info.Mode().Perm())
	}
}

func TestWriteFile_RefusesDirectory(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootDir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	if err := WriteFile(cfg, "adir", []byte("x"), 0o644, 1024, 5*time.Second); !errors.Is(err, ErrFileIsADir) {
		t.Fatalf("want ErrFileIsADir, got %v", err)
	}
}

// TestWriteFile_RefusesOverCap is the VER-16 proof for the write-side bound
// (PERF-17/18): an over-cap payload against a target that would refuse any
// connection attempt (bad host/port, no credentials) still returns
// ErrFileOverCap specifically. If the cap were checked after dialing or after
// opening the remote file, this would instead surface a dial/auth error —
// seeing ErrFileOverCap proves the check runs before any network I/O, per
// ADR-0071 §D5 ("before opening the remote file").
func TestWriteFile_RefusesOverCap(t *testing.T) {
	cfg := Config{
		Host: "127.0.0.1", Port: "1", User: "nobody",
		HostKey: "", PrivateKey: "", DialTimeout: 100 * time.Millisecond,
	}

	err := WriteFile(cfg, "whatever.bin", bytes.Repeat([]byte("z"), 10), 0o644, 9, 5*time.Second)
	if !errors.Is(err, ErrFileOverCap) {
		t.Fatalf("want ErrFileOverCap without ever dialing, got %v", err)
	}
}

// --- ListDir ---

func TestListDir_HappyPath(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rootDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	entries, err := ListDir(cfg, ".", 5*time.Second)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	byName := map[string]FileEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	f, ok := byName["a.txt"]
	if !ok || f.IsDir || f.Size != 1 {
		t.Fatalf("a.txt entry = %+v, ok=%v", f, ok)
	}
	d, ok := byName["sub"]
	if !ok || !d.IsDir {
		t.Fatalf("sub entry = %+v, ok=%v", d, ok)
	}
}

func TestListDir_RefusesNonDirectory(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	addr, hostKeyLine := startSSHServer(t, sftpSubsystemHandler(rootDir))
	cfg := testConfig(t, addr, hostKeyLine)

	if _, err := ListDir(cfg, "a.txt", 5*time.Second); !errors.Is(err, ErrFileNotADir) {
		t.Fatalf("want ErrFileNotADir, got %v", err)
	}
}

// --- Timeout (withSFTP, shared by all three operations) ---

// TestWithSFTP_TimesOut is the VER-16 proof for the per-operation timeout
// (PERF-19): against a server that accepts the SFTP subsystem request but
// then never responds to the protocol handshake, ListDir must return
// ErrFileTimeout — specifically that error, not a generic transport failure —
// and must do so promptly, not after Go's own test-level deadline. That
// distinguishes "our timeout fired" from "the operation eventually finished
// on its own".
func TestWithSFTP_TimesOut(t *testing.T) {
	addr, hostKeyLine := startSSHServer(t, hangingSubsystemHandler)
	cfg := testConfig(t, addr, hostKeyLine)

	start := time.Now()
	_, err := ListDir(cfg, ".", 100*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrFileTimeout) {
		t.Fatalf("want ErrFileTimeout, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took too long to fire (%s) — the guard did not actually unblock", elapsed)
	}
}

// --- mapSFTPError ---
//
// Permission-denied is exercised at this logic level only, not through the
// real in-process transport above: the Docker test runner executes as root,
// which bypasses real file-mode enforcement (a chmod-0000 fixture would still
// be readable), so a genuine SSH_FX_PERMISSION_DENIED cannot be reproduced
// that way in this environment. pkg/sftp's client already normalizes that
// wire status to os.ErrPermission before it reaches this package (its own
// normaliseError, unexported); what these tests cover is this package's own
// translation of that normalized form into ErrFileDenied.

func TestMapSFTPError_PermissionDenied(t *testing.T) {
	wrapped := fmt.Errorf("stat: %w", os.ErrPermission)
	if err := mapSFTPError(wrapped); !errors.Is(err, ErrFileDenied) {
		t.Fatalf("want ErrFileDenied, got %v", err)
	}
}

func TestMapSFTPError_NotFound(t *testing.T) {
	wrapped := fmt.Errorf("stat: %w", os.ErrNotExist)
	if err := mapSFTPError(wrapped); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("want ErrFileNotFound, got %v", err)
	}
}
