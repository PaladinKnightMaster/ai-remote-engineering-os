//go:build integration

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"

	"aireos.local/services/connector-service/internal/store"
)

const secKey = "http-sec66-resource-key-0123456789ab"

// forms: every encoding of a private key a leak could take.
func forms(t *testing.T, pem string) map[string][]byte {
	t.Helper()
	raw, err := ssh.ParseRawPrivateKey([]byte(pem))
	if err != nil {
		t.Fatalf("parse leased key: %v", err)
	}
	sk := *(raw.(*ed25519.PrivateKey))
	body := strings.NewReplacer("-----BEGIN OPENSSH PRIVATE KEY-----", "", "-----END OPENSSH PRIVATE KEY-----", "", "\n", "").Replace(pem)
	return map[string][]byte{
		"pem": []byte(pem), "pem-body": []byte(body),
		"raw-key": []byte(sk), "raw-seed": sk.Seed(),
		"b64-key": []byte(base64.StdEncoding.EncodeToString(sk)), "b64-seed": []byte(base64.StdEncoding.EncodeToString(sk.Seed())),
	}
}

func findLeaks(hay []byte, all []map[string][]byte) []string {
	var found []string
	for _, fs := range all {
		for name, needle := range fs {
			if bytes.Contains(hay, needle) {
				found = append(found, name)
			}
		}
	}
	return found
}

// SEC-66: no private-key encoding in any response, outbox payload or log line
// across generate/rotate/pending/bind; the search is proven on planted keys first.
func TestSEC66NoPrivateKeyBytesLeave(t *testing.T) {
	dsn := os.Getenv("CONNECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CONNECTOR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	idA, idB := uuid.NewString(), uuid.NewString()
	label := "sec66-" + idA
	t.Cleanup(func() {
		for _, id := range []string{idA, idB} {
			_, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM connector_outbox WHERE payload->>'target'=$1`, "resource:"+id)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM connector_pending_credentials WHERE label=$1`, label)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_outbox WHERE payload->>'target'=$1`, "credential-pending:"+label)
	})

	srv := httptest.NewServer(New(Config{Pool: pool, Verifier: apiVerifier{}, SecretKey: secKey}))
	defer srv.Close()
	var bodies bytes.Buffer
	call := func(method, path, body string, want int) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		bodies.Write(b)
		for k, v := range resp.Header {
			bodies.WriteString(k + ": " + strings.Join(v, ",") + "\n")
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s = %d (%s), want %d", method, path, resp.StatusCode, b, want)
		}
	}
	lease := func(id string) map[string][]byte {
		m, err := store.Lease(ctx, pool, id, "sess", "operator:admin", secKey)
		if err != nil {
			t.Fatalf("lease %s: %v", id, err)
		}
		return forms(t, m.PrivateKey)
	}

	call("POST", "/v1/credentials/"+idA+"/generate", `{"rotate":false,"operator_subject":"operator:admin"}`, 200)
	var keys []map[string][]byte
	keys = append(keys, lease(idA)) // pre-rotation key
	call("POST", "/v1/credentials/"+idA+"/generate", `{"rotate":false,"operator_subject":"operator:admin"}`, 409)
	call("POST", "/v1/credentials/"+idA+"/generate", `{"rotate":true,"operator_subject":"operator:admin"}`, 200)
	keys = append(keys, lease(idA)) // post-rotation key
	call("GET", "/v1/credentials/"+idA+"/public", ``, 200)
	call("GET", "/v1/credentials/"+idB+"/public", ``, 404)
	if _, err := store.GeneratePending(ctx, pool, label, "aireos-demo", "ssh-ed25519 AAAAhost", secKey, "system:setup"); err != nil {
		t.Fatal(err)
	}
	call("GET", "/v1/credentials/pending/"+label, ``, 200)
	call("POST", "/v1/credentials/pending/"+label+"/bind", `{"resource_id":"`+idB+`","operator_subject":"operator:admin"}`, 200)
	call("POST", "/v1/credentials/pending/"+label+"/bind", `{"resource_id":"`+idB+`","operator_subject":"operator:admin"}`, 404)
	call("GET", "/v1/credentials/"+idB+"/public", ``, 200)
	keys = append(keys, lease(idB)) // the bound key

	for target, want := range map[string][2]string{"resource:" + idA: {"credential.rotated", "operator:admin"}, "credential-pending:" + label: {"credential.pending", "system:setup"}} {
		var actor string
		if err := pool.QueryRow(ctx, `SELECT payload->>'actor' FROM connector_outbox WHERE payload->>'target'=$1 AND payload->>'outcome'=$2`, target, want[0]).Scan(&actor); err != nil || actor != want[1] {
			t.Fatalf("%s actor for %s = %q (%v), want %q", want[0], target, actor, err, want[1])
		}
	}
	log.Print("sec66-capture-control") // the log capture is live, so an empty log search means something
	if !bytes.Contains(logBuf.Bytes(), []byte("sec66-capture-control")) {
		t.Fatal("log capture is not working")
	}
	var hay bytes.Buffer
	hay.Write(bodies.Bytes())
	hay.Write(logBuf.Bytes())
	for _, id := range []string{idA, idB} {
		rows, err := pool.Query(ctx, `SELECT payload::text FROM connector_outbox WHERE payload->>'target'=$1`, "resource:"+id)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var p string
			_ = rows.Scan(&p)
			hay.WriteString(p)
		}
		rows.Close()
	}
	if hay.Len() < 500 {
		t.Fatalf("haystack suspiciously small (%d bytes) — the capture is not working", hay.Len())
	}

	// Planted controls: the search MUST find a planted instance of every form
	// before its "nothing found" is believed.
	for _, k := range keys {
		for name, needle := range k {
			if got := findLeaks(append(append([]byte{}, hay.Bytes()...), needle...), []map[string][]byte{k}); !slices.Contains(got, name) {
				t.Fatalf("negative control FAILED: planted %q not detected (found %v)", name, got)
			}
		}
	}
	if leaks := findLeaks(hay.Bytes(), keys); len(leaks) != 0 {
		t.Fatalf("SEC-66: private key material found in responses/outbox/log: %v", leaks)
	}
	t.Logf("SEC-66: %d bytes searched (responses+outbox+log), %d keys x %d encodings, planted controls detected, 0 leaks",
		hay.Len(), len(keys), len(keys[0]))
}
