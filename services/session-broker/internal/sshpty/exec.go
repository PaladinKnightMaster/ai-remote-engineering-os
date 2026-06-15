// Non-interactive command exec over SSH (ADR-0058), beside the interactive PTY
// path. It reuses the same host-key-pinned dial (dialClient) + leased credential,
// runs ONE command (no PTY, no shell), and returns its captured stdout/stderr
// bounded to maxBytes under a command timeout. The broker stays the sole SSH
// egress engine; the gateway never dials SSH.
package sshpty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// ExecResult is the outcome of a non-interactive command run.
type ExecResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int  // the remote command's exit status (0 on success)
	Truncated bool // output exceeded maxBytes and was capped (PERF-1)
}

// Exec dials cfg's target (pinned host key + key auth), runs command once, and
// returns its bounded output. A non-zero exit is NOT an error (ExitCode carries
// it); a dial/transport failure or a timeout is. maxBytes ≤ 0 means no cap;
// timeout ≤ 0 defaults to 60s.
func Exec(cfg Config, command string, maxBytes int, timeout time.Duration) (ExecResult, error) {
	client, err := dialClient(cfg)
	if err != nil {
		return ExecResult{}, err
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		return ExecResult{}, fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	outBuf := &cappedBuffer{max: maxBytes}
	errBuf := &cappedBuffer{max: maxBytes}
	sess.Stdout = outBuf
	sess.Stderr = errBuf

	if err := sess.Start(command); err != nil {
		return ExecResult{}, fmt.Errorf("start command: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	to := orDur(timeout, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done // Wait returns once the session is closed → buffers are stable
		return ExecResult{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes(), Truncated: outBuf.truncated || errBuf.truncated},
			fmt.Errorf("command timed out after %s", to)
	case werr := <-done:
		exitCode := 0
		if werr != nil {
			var ee *ssh.ExitError
			if errors.As(werr, &ee) {
				exitCode = ee.ExitStatus() // non-zero exit, not a transport error
			} else {
				return ExecResult{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes(), Truncated: outBuf.truncated || errBuf.truncated},
					fmt.Errorf("command: %w", werr)
			}
		}
		return ExecResult{
			Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes(),
			ExitCode: exitCode, Truncated: outBuf.truncated || errBuf.truncated,
		}, nil
	}
}

// cappedBuffer captures up to max bytes, discarding the rest and flagging
// truncation (PERF-1). It always reports a full write so the SSH output copier is
// never blocked by the cap. Written by a single copier goroutine per stream, so it
// needs no lock. max ≤ 0 means unbounded.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.max <= 0 {
		return c.buf.Write(p)
	}
	room := c.max - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }
