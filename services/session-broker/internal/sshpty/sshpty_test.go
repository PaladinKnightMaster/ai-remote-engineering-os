package sshpty

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
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

// startMultiKeyServer brings up an in-process SSH server offering BOTH an ECDSA
// and an Ed25519 host key (in that order), and returns its address plus the
// authorized_keys line for each. Real OpenSSH servers offer multiple host-key
// types by default (`ssh-keygen -A` generates ed25519+ecdsa+rsa); a single-key
// fixture like startServer above can never exercise host-key ALGORITHM
// negotiation, which is exactly why the T-123 mismatch bug shipped undetected —
// there was never more than one type on offer to disambiguate.
func startMultiKeyServer(t *testing.T) (addr, ecdsaLine, ed25519Line string) {
	t.Helper()
	ecdsaPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaPriv)
	if err != nil {
		t.Fatalf("ecdsa signer: %v", err)
	}
	ecdsaLine = string(ssh.MarshalAuthorizedKey(ecdsaSigner.PublicKey()))

	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 key: %v", err)
	}
	edSigner, err := ssh.NewSignerFromKey(edPriv)
	if err != nil {
		t.Fatalf("ed25519 signer: %v", err)
	}
	ed25519Line = string(ssh.MarshalAuthorizedKey(edSigner.PublicKey()))

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	// ECDSA added first: x/crypto/ssh's default (unconstrained) preference
	// order picks ecdsa-sha2-nistp256 over ssh-ed25519, which is precisely the
	// ordering that made the unfixed dialClient negotiate the wrong key type
	// when an operator had pinned the ed25519 key.
	cfg.AddHostKey(ecdsaSigner)
	cfg.AddHostKey(edSigner)

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
	return ln.Addr().String(), ecdsaLine, ed25519Line
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

// TestDialClientPinsNonPreferredHostKeyType is the T-123 regression test.
//
// The bug: dialClient left ssh.ClientConfig.HostKeyAlgorithms unset, so
// x/crypto/ssh negotiated the host-key algorithm from its OWN internal
// preference order (ecdsa-sha2-nistp256 before ssh-ed25519), independent of
// which key type the operator pinned. Against a server offering both — which
// is what every real OpenSSH target does — the server returns its ECDSA key
// per that negotiated preference, ssh.FixedHostKey compares it against the
// pinned Ed25519 key, and the handshake dies with "ssh: host key mismatch"
// even though the pinned key is present and correct on the server.
//
// Run this test against the pre-fix dialClient (HostKeyAlgorithms unset): it
// FAILS with exactly that mismatch error. After constraining
// HostKeyAlgorithms to the pinned key's own type, it PASSES. A single-key test
// fixture (TestDialClientPinsHostKey above) cannot distinguish these two
// behaviors, which is why the defect shipped undetected since v0.1.0.
func TestDialClientPinsNonPreferredHostKeyType(t *testing.T) {
	addr, _, ed25519Line := startMultiKeyServer(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	cfg := Config{
		Host: host, Port: port, User: "u",
		HostKey: ed25519Line, PrivateKey: clientKeyPEM(t),
		DialTimeout: 5 * time.Second,
	}
	client, err := dialClient(cfg)
	if err != nil {
		t.Fatalf("dial pinning the non-preferred (ed25519) host key type against a "+
			"server offering ecdsa+ed25519: %v", err)
	}
	_ = client.Close()
}

// TestDialClientRejectsUnpinnedKeyAmongMultiple keeps the fail-closed property
// covered under the multi-key fixture: pinning a key the server does not hold
// must still be rejected, even when the server offers other, valid keys.
func TestDialClientRejectsUnpinnedKeyAmongMultiple(t *testing.T) {
	addr, _, _ := startMultiKeyServer(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	cfg := Config{
		Host: host, Port: port, User: "u",
		HostKey: otherHostKeyLine(t), PrivateKey: clientKeyPEM(t),
		DialTimeout: 5 * time.Second,
	}
	if _, err := dialClient(cfg); err == nil {
		t.Fatal("dial pinning a key the server does not hold must fail, even with multiple host keys offered")
	}
}

// RSA note: hostKeyAlgorithms' RSA branch (rsa-sha2-256/512 + legacy ssh-rsa
// for a pinned "ssh-rsa"-typed key) is intentionally not covered by an
// in-process server test here — x/crypto/ssh's ServerConfig negotiates
// signature algorithm from the client's offered list using the server's own
// preference, and reliably forcing it to sign with legacy ssh-rsa (SHA-1)
// instead of rsa-sha2-256 to distinguish the branches would require reaching
// into unexported negotiation internals. That's more test-harness machinery
// than the payoff justifies; the branch is exercised by the RSA arm of
// hostKeyAlgorithms itself (a pure function, trivially inspectable) and by
// real-world OpenSSH 8.8+ targets pinned on an RSA key.

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
