// Command api is the operator-facing gateway (ADR-0043). It serves the resource
// registry (T-005b) gated by capability tokens; operator login (ADR-0046) lands
// in a later auth ticket.
//
// Subcommands:
//
//	api serve                       # default; run the HTTP server
//	api migrate up|version|force <v>
//
// (The shared migrate subcommand exposes up|version|force; wiring a `down`
// action for standard up/down services like api is a small follow-up on
// packages/go/migrate — the 0001 down.sql file already exists.)
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/migrate"
	"aireos.local/services/api/internal/server"
	"aireos.local/services/api/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("api: %v", err)
	}
}

func run(args []string) error {
	dsn := firstNonEmpty(os.Getenv("API_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("API_DATABASE_URL (or DATABASE_URL) must be set")
	}
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd == "migrate" {
		return migrate.RunSubcommand(migrate.Runner{
			Service: "api",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	}
	if cmd != "serve" {
		return fmt.Errorf("unknown command %q (want serve|migrate)", cmd)
	}
	return serve(dsn)
}

func serve(dsn string) error {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	v, err := capabilityVerifier()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("API_ADDR"), ":8080"),
		Handler:           server.New(store.New(pool), v),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("api listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// capabilityVerifier resolves the capability Verifier (ADR-0046): real PASETO
// verifier (audience "api") when AIREOS_PASETO_KEY is set, else the dev allow-all
// verifier with API_DEV_INSECURE_AUTH=1, else fail loud.
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID") // optional; empty skips the issuer rule
		return auth.AsMiddlewareVerifier(auth.NewVerifier(key, issuer, "api")), nil
	}
	if os.Getenv("API_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: API_DEV_INSECURE_AUTH=1 — using dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY (hex cluster key, ADR-0046) or API_DEV_INSECURE_AUTH=1 for local dev")
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "human-agent"}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
