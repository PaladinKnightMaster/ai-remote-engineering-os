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
