//go:build integration

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/connector-service/internal/store"
)

// T-176c1: a credential imported with set-secret has no recorded public key, and
// GET .../public says so in words distinct from "no such credential" - the cockpit
// must be able to tell an operator-imported key from a missing one.
func TestImportedCredentialHasADistinctPublicKey404(t *testing.T) {
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
	imported, missing := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, imported)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_outbox WHERE payload->>'target'=$1`, "resource:"+imported)
	})
	if err := store.Set(ctx, pool, imported, secKey, store.Material{PrivateKey: "operator's own key"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(Config{Pool: pool, Verifier: apiVerifier{}, SecretKey: secKey}))
	defer srv.Close()
	get := func(id string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+"/v1/credentials/"+id+"/public", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(imported); code != 404 || !strings.Contains(body, "imported") {
		t.Errorf("imported credential: %d %q, want 404 mentioning \"imported\"", code, body)
	}
	if code, body := get(missing); code != 404 || strings.Contains(body, "imported") {
		t.Errorf("missing credential: %d %q, want 404 NOT mentioning \"imported\"", code, body)
	}
}
