// bridgespike — THROWAWAY spike stub for the aireos-mcp-bridge check (2026-11-18).
// NOT product code. Serves:
//   - the REAL gateway handler (internal/server.New, unchanged) over a stub Catalog
//     with one remote.exec tool; "slow" commands sleep SLOW_SECONDS to stand in for
//     a human approval wait (block-and-wait). Nothing governed runs.
//   - a stub of ADR-0080's POST /v1/agent-tokens/exchange (the api side): Bearer
//     enrollment credential in → the real auth.AsymmetricSigner.IssueAgent 5-minute
//     token out. Credential compared by SHA-256 hash only.
// Both over TLS with a leaf signed by a throwaway CA (root NOT sent in the chain,
// mirroring Caddy `tls internal`).
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"aireos.local/packages/go/auth"
	"aireos.local/services/mcp-gateway/internal/mcp"
	"aireos.local/services/mcp-gateway/internal/server"
)

var slowSeconds = 130

type stubCatalog struct{ n atomic.Int64 }

func (c *stubCatalog) List() []mcp.Tool {
	return []mcp.Tool{{
		Name:        "remote.exec",
		Description: "Run a shell command on the target (spike stub: nothing runs).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}}
}

func (c *stubCatalog) Invoke(ctx context.Context, name string, args json.RawMessage) (mcp.ToolResult, error) {
	if name != "remote.exec" {
		return mcp.ToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &a)
	n := c.n.Add(1)
	start := time.Now()
	log.Printf("EVENT invoke.start n=%d command=%q", n, a.Command)
	if strings.Contains(a.Command, "slow") {
		select {
		case <-time.After(time.Duration(slowSeconds) * time.Second):
		case <-ctx.Done():
			log.Printf("EVENT invoke.cancelled n=%d after=%s err=%v", n, time.Since(start).Round(time.Second), ctx.Err())
			return mcp.ToolResult{}, ctx.Err()
		}
	}
	log.Printf("EVENT invoke.done n=%d after=%s", n, time.Since(start).Round(time.Second))
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: fmt.Sprintf("ok #%d: %s", n, a.Command)}}}, nil
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

func logged(name string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		start := time.Now()
		h.ServeHTTP(rec, r)
		log.Printf("EVENT http %s %s %s status=%d took=%s", name, r.Method, r.URL.Path, rec.code, time.Since(start).Round(time.Millisecond))
	})
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	out := os.Getenv("SPIKE_OUT") // shared dir: ca.pem, ca.sha256, credential
	if v, err := strconv.Atoi(os.Getenv("SLOW_SECONDS")); err == nil {
		slowSeconds = v
	}

	// ---- throwaway CA + leaf (root not served) ----
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "spike throwaway root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	must(err)
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "gateway"},
		DNSNames: []string{"gateway", "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	must(err)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	must(os.WriteFile(out+"/ca.pem", caPEM, 0o644))
	fp := sha256.Sum256(caDER)
	must(os.WriteFile(out+"/ca.sha256", []byte(hex.EncodeToString(fp[:])+"\n"), 0o644))
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}

	// ---- signer (api side) / verifier (gateway side, keyless of the secret) ----
	sk, pk, _, _ := auth.GenerateAsymmetricKey()
	signer := auth.NewAsymmetricSigner(sk, "spike-cluster")
	verifier := auth.NewAsymmetricVerifier(pk, "spike-cluster", "mcp-gateway")

	// ---- enrollment credential: shown once (written to the shared dir), stored as hash ----
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	cred := "aireos_agent_" + strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(
		fmt.Sprintf("%x", raw)), "=")
	credHash := sha256.Sum256([]byte(cred))
	must(os.WriteFile(out+"/credential", []byte(cred+"\n"), 0o600))

	var exchanges atomic.Int64
	exchange := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		h := sha256.Sum256([]byte(got))
		if r.Method != http.MethodPost || subtle.ConstantTimeCompare(h[:], credHash[:]) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid credential"}`))
			return
		}
		tok, err := signer.IssueAgent("agent:cline-spike", "mcp-gateway", auth.Scope{Resource: "res-spike", Operations: []string{"exec"}})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		n := exchanges.Add(1)
		log.Printf("EVENT agent.token.exchanged n=%d subject=agent:cline-spike expires_in=%ds", n, int(auth.AgentTTL.Seconds()))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expiresIn": int(auth.AgentTTL.Seconds())})
	})

	gw := server.New(server.Config{Verifier: auth.AsMiddlewareVerifier(verifier), Catalog: &stubCatalog{}})
	go func() {
		s := &http.Server{Addr: ":8444", Handler: logged("api", exchange), TLSConfig: tlsCfg}
		log.Fatal(s.ListenAndServeTLS("", ""))
	}()
	log.Printf("EVENT ready agentTTL=%s slow=%ds", auth.AgentTTL, slowSeconds)
	s := &http.Server{Addr: ":8443", Handler: logged("gateway", gw), TLSConfig: tlsCfg}
	log.Fatal(s.ListenAndServeTLS("", ""))
}
