package sshpty

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startServer brings up an in-process SSH server on a random localhost port that
// accepts any client key, and returns its address + its host key as an
// authorized_keys line (what a target's resources.labels['ssh_host_key'] holds).
func startServer(t *testing.T) (addr, hostKeyLine string) {
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
				go func() {
					for ch := range chans {
						_ = ch.Reject(ssh.Prohibited, "test server")
					}
				}()
				_ = sc
			}()
		}
	}()
	return ln.Addr().String(), hostKeyLine
}

func clientKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func otherHostKeyLine(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("other key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh pub: %v", err)
	}
	return string(ssh.MarshalAuthorizedKey(sshPub))
}

// TestDialClientPinsHostKey — the correct pinned host key handshakes; a different
// pinned key is rejected (FixedHostKey, replacing the spike's InsecureIgnoreHostKey).
func TestDialClientPinsHostKey(t *testing.T) {
	addr, hostKeyLine := startServer(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	base := Config{
		Host: host, Port: port, User: "u",
		HostKey: hostKeyLine, PrivateKey: clientKeyPEM(t),
		DialTimeout: 5 * time.Second,
	}

	client, err := dialClient(base)
	if err != nil {
		t.Fatalf("dial with correct pinned host key: %v", err)
	}
	_ = client.Close()

	bad := base
	bad.HostKey = otherHostKeyLine(t)
	if _, err := dialClient(bad); err == nil {
		t.Fatal("dial with a different pinned host key must fail")
	}
}

// lineBuffer yields the operator-attributable atoms for terminal.input.line
// (ADR-0048 §6): raw bytes, no normalization, Enter-delimited, empty lines
// dropped — mirroring the spike's validated scenarios.
func TestLineBuffer(t *testing.T) {
	toStr := func(lines [][]byte) []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = string(l)
		}
		return out
	}

	t.Run("two commands across one frame", func(t *testing.T) {
		var lb lineBuffer
		got := toStr(lb.feed([]byte("echo first\recho second\r")))
		if len(got) != 2 || got[0] != "echo first" || got[1] != "echo second" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("backspace is preserved raw, not normalized", func(t *testing.T) {
		var lb lineBuffer
		got := toStr(lb.feed([]byte("helo\bllo\r")))
		if len(got) != 1 || got[0] != "helo\bllo" {
			t.Fatalf("got %q, want raw helo<BS>llo", got)
		}
	})

	t.Run("empty Enter yields nothing", func(t *testing.T) {
		var lb lineBuffer
		if got := lb.feed([]byte("\r\r\r")); len(got) != 0 {
			t.Fatalf("want 0 lines, got %d", len(got))
		}
	})

	t.Run("line split across frames", func(t *testing.T) {
		var lb lineBuffer
		if got := lb.feed([]byte("ec")); len(got) != 0 {
			t.Fatalf("partial frame should yield nothing, got %v", got)
		}
		got := toStr(lb.feed([]byte("ho hi\r")))
		if len(got) != 1 || got[0] != "echo hi" {
			t.Fatalf("got %q, want reassembled 'echo hi'", got)
		}
	})
}

func TestParseControlResize(t *testing.T) {
	cm, ok := parseControl([]byte(`{"type":"resize","cols":120,"rows":40}`))
	if !ok || cm.Type != "resize" || cm.Cols != 120 || cm.Rows != 40 {
		t.Fatalf("resize parse: %+v ok=%v", cm, ok)
	}
	if _, ok := parseControl([]byte("not json")); ok {
		t.Fatal("non-JSON should not parse")
	}
	if _, ok := parseControl([]byte(`{}`)); ok {
		t.Fatal("empty type should be rejected")
	}
}
