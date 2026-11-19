// aireos-mcp-bridge — THROWAWAY PROTOTYPE for the ADR-0080 revision check (2026-11-18).
// stdio (newline-delimited JSON-RPC, what every MCP client launches) <-> HTTPS POST to the
// gateway's /mcp. Holds the enrollment credential, exchanges it for the 5-minute agent token,
// renews proactively (age >= RENEW_AFTER) and reactively (one retry on 401), and trusts ONLY
// the operator-downloaded CA root whose SHA-256 matches the pinned fingerprint.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	gatewayURL  = os.Getenv("AIREOS_GATEWAY_URL")
	exchangeURL = os.Getenv("AIREOS_EXCHANGE_URL")
	noRefresh   = os.Getenv("AIREOS_BRIDGE_NO_REFRESH") == "1" // negative control only
	renewAfter  = 4 * time.Minute
	client      *http.Client
	credential  string

	mu       sync.Mutex
	token    string
	tokenAt  time.Time
	outMu    sync.Mutex
	exchgCnt int
)

func fatal(f string, a ...any) { log.Printf("FATAL "+f, a...); os.Exit(1) }

func loadPinnedRoot() *x509.CertPool {
	pemBytes, err := os.ReadFile(os.Getenv("AIREOS_CA_FILE"))
	if err != nil {
		fatal("read CA file: %v", err)
	}
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		fatal("CA file is not PEM")
	}
	sum := sha256.Sum256(blk.Bytes)
	want := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(os.Getenv("AIREOS_CA_SHA256")), ":", ""))
	if hex.EncodeToString(sum[:]) != want {
		fatal("CA fingerprint MISMATCH: file=%x pinned=%s — refusing to connect", sum, want)
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		fatal("parse CA: %v", err)
	}
	pool := x509.NewCertPool() // ONLY this root; the system store is not consulted
	pool.AddCert(cert)
	log.Printf("EVENT ca.pinned sha256=%x", sum)
	return pool
}

// getToken returns a usable token; force=true after a 401.
func getToken(force bool) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	fresh := token != "" && time.Since(tokenAt) < renewAfter
	if token != "" && (noRefresh || (!force && fresh)) {
		return token, nil
	}
	req, _ := http.NewRequest(http.MethodPost, exchangeURL, nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("exchange HTTP %d: %s", resp.StatusCode, b)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Token == "" {
		return "", fmt.Errorf("exchange: bad body")
	}
	reason := "initial"
	if token != "" {
		reason = map[bool]string{true: "reactive-401", false: "proactive-age"}[force]
	}
	exchgCnt++
	token, tokenAt = body.Token, time.Now()
	log.Printf("EVENT token.renewed n=%d reason=%s", exchgCnt, reason)
	return token, nil
}

func post(line []byte, tok string) (int, []byte, error) {
	req, _ := http.NewRequest(http.MethodPost, gatewayURL, bytes.NewReader(line))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func write(b []byte) {
	outMu.Lock()
	defer outMu.Unlock()
	os.Stdout.Write(append(bytes.TrimRight(b, "\n"), '\n'))
}

func handle(line []byte) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(line, &msg)
	start := time.Now()
	status, body, err := 0, []byte(nil), error(nil)
	tok, terr := getToken(false)
	if terr == nil {
		status, body, err = post(line, tok)
		if err == nil && status == http.StatusUnauthorized && !noRefresh {
			log.Printf("EVENT gateway.401 method=%s — renewing and retrying once", msg.Method)
			if tok, terr = getToken(true); terr == nil {
				status, body, err = post(line, tok)
			}
		}
	}
	if terr != nil {
		err = terr
	}
	log.Printf("EVENT forward method=%s id=%s status=%d took=%s err=%v", msg.Method, msg.ID, status, time.Since(start).Round(time.Millisecond), err)
	if len(msg.ID) == 0 || string(msg.ID) == "null" { // notification: nothing to answer
		return
	}
	if err == nil && status == http.StatusOK {
		write(body)
		return
	}
	text := fmt.Sprintf("aireos-mcp-bridge: gateway HTTP %d %s", status, strings.TrimSpace(string(body)))
	if err != nil {
		text = "aireos-mcp-bridge: " + err.Error()
	}
	e, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32000, "message": text}})
	write(e)
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if p := os.Getenv("AIREOS_BRIDGE_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}
	c, err := os.ReadFile(os.Getenv("AIREOS_CREDENTIAL_FILE"))
	if err != nil {
		fatal("read credential: %v", err)
	}
	credential = strings.TrimSpace(string(c))
	client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: loadPinnedRoot(), MinVersion: tls.VersionTLS12}}}
	log.Printf("EVENT bridge.start pid=%d noRefresh=%v", os.Getpid(), noRefresh)
	r := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			go handle(bytes.TrimSpace(line))
		}
		if err != nil {
			log.Printf("EVENT bridge.stdin-closed err=%v", err)
			time.Sleep(time.Second) // let in-flight replies drain
			return
		}
	}
}
