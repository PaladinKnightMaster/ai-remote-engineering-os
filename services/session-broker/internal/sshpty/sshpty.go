// Package sshpty is the broker's SSH-over-WebSocket data plane: it dials a target
// with a pinned host key + public-key auth, allocates a PTY, and pipes bytes
// between the WebSocket and the remote shell while recording the session.
//
// Productionizes the spike (docs/spike/xterm-ssh-pty-findings.md), closing its
// named gaps: host-key pinning (ssh.FixedHostKey, not InsecureIgnoreHostKey),
// PTY resize signaling, idle-timeout enforcement, and bounded large-output
// streaming. WS framing: binary frames are raw PTY bytes; text frames are JSON
// control messages ({"type":"resize","cols":C,"rows":R}).
package sshpty

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

// Config is everything needed to dial + open a shell on a target.
type Config struct {
	Host, Port, User       string
	HostKey                string // authorized_keys line, pinned via ssh.FixedHostKey
	PrivateKey, Passphrase string // decrypted target credential
	Cols, Rows             int    // initial PTY size (defaults 80x24)
	DialTimeout            time.Duration
}

// Recorder receives the session's output and input byte streams (the cast file).
type Recorder interface {
	Record(input bool, data []byte) error
}

// Conn is a live SSH session + PTY.
type Conn struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

// Dial establishes the SSH connection (pinned host key + key auth), allocates a
// PTY, and starts the login shell.
func Dial(cfg Config) (*Conn, error) {
	client, err := dialClient(cfg)
	if err != nil {
		return nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	cols, rows := orInt(cfg.Cols, 80), orInt(cfg.Rows, 24)
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}
	return &Conn{client: client, session: sess, stdin: stdin, stdout: stdout}, nil
}

// dialClient performs the SSH handshake with the pinned host key. It is the unit
// the host-key-pinning test exercises directly.
func dialClient(cfg Config) (*ssh.Client, error) {
	signer, err := parseKey(cfg)
	if err != nil {
		return nil, err
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cfg.HostKey))
	if err != nil {
		return nil, fmt.Errorf("parse pinned host key: %w", err)
	}
	conf := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
		Timeout:         orDur(cfg.DialTimeout, 10*time.Second),
	}
	addr := net.JoinHostPort(cfg.Host, orStr(cfg.Port, "22"))
	client, err := ssh.Dial("tcp", addr, conf)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return client, nil
}

func parseKey(cfg Config) (ssh.Signer, error) {
	if cfg.Passphrase != "" {
		s, err := ssh.ParsePrivateKeyWithPassphrase([]byte(cfg.PrivateKey), []byte(cfg.Passphrase))
		if err != nil {
			return nil, fmt.Errorf("parse private key (passphrase): %w", err)
		}
		return s, nil
	}
	s, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return s, nil
}

// control is a WS text-frame control message.
type control struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func parseControl(b []byte) (control, bool) {
	var c control
	if err := json.Unmarshal(b, &c); err != nil {
		return control{}, false
	}
	return c, c.Type != ""
}

// Pipe shuttles bytes between the WebSocket and the PTY until either side closes,
// the context is canceled, or idle elapses with no operator input. It records
// output ("o") and input ("i") to rec and returns the close reason (one of the
// sessions.closed_reason CHECK values: idle|operator|broker_crash).
func (c *Conn) Pipe(ctx context.Context, ws *websocket.Conn, rec Recorder, idle time.Duration) string {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var once sync.Once
	reason := "operator"
	set := func(r string) {
		once.Do(func() { reason = r })
		cancel()
	}

	var idleTimer *time.Timer
	if idle > 0 {
		idleTimer = time.AfterFunc(idle, func() { set("idle") })
		defer idleTimer.Stop()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// PTY -> WS (output). Bounded buffer; coder/websocket Write applies
	// backpressure, so a flood of output cannot OOM the broker.
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := c.stdout.Read(buf)
			if n > 0 {
				_ = rec.Record(false, buf[:n])
				if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					set("operator") // client gone
					return
				}
			}
			if rerr != nil {
				set("operator") // remote shell exited / pipe closed
				return
			}
		}
	}()

	// WS -> PTY (input + control).
	go func() {
		defer wg.Done()
		for {
			typ, data, rerr := ws.Read(ctx)
			if rerr != nil {
				set("operator") // client closed the channel
				return
			}
			if idleTimer != nil {
				idleTimer.Reset(idle)
			}
			if typ == websocket.MessageText {
				if cm, ok := parseControl(data); ok && cm.Type == "resize" {
					_ = c.session.WindowChange(orInt(cm.Rows, 24), orInt(cm.Cols, 80))
				}
				continue
			}
			_ = rec.Record(true, data)
			if _, werr := c.stdin.Write(data); werr != nil {
				set("broker_crash")
				return
			}
		}
	}()

	<-ctx.Done()
	// Closing the session unblocks the output goroutine's stdout.Read.
	_ = c.session.Close()
	wg.Wait()
	return reason
}

// Close tears down the session and client.
func (c *Conn) Close() error {
	if c.session != nil {
		_ = c.session.Close()
	}
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

func orInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orDur(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
