// Command audit-service is the append-only audit chain authority (ADR-0045).
//
// Subcommands (ADR-0044):
//
//	audit-service serve                 # default; run the HTTP server
//	audit-service migrate up|version|force <v>
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

	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/migrate"
	"aireos.local/services/audit-service/internal/server"
	"aireos.local/services/audit-service/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("audit-service: %v", err)
	}
}

func run(args []string) error {
	dsn := firstNonEmpty(os.Getenv("AUDIT_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("AUDIT_DATABASE_URL (or DATABASE_URL) must be set")
	}

	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	if cmd == "migrate" {
		return migrate.RunSubcommand(migrate.Runner{
			Service: "audit-service",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
			// OnApply audit-emission into the chain is a documented P0 gap
			// (ADR-0044 §Verification); the hook is available for T-008+.
		}, args[1:])
	}
	if cmd != "serve" {
		return fmt.Errorf("unknown command %q (want serve|migrate)", cmd)
	}
	return serve(dsn)
}

func serve(dsn string) error {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	v, err := capabilityVerifier()
	if err != nil {
		return err
	}

	h := server.New(store.New(pool), v)
	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("AUDIT_ADDR"), ":8081"),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("audit-service listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// capabilityVerifier resolves the capability Verifier (ADR-0046). The real
// PASETO verifier (packages/go/auth/) lands in T-008. Until then the service
// FAILS LOUD: it refuses to serve unless AUDIT_DEV_INSECURE_AUTH=1 explicitly
// opts into the dev-only allow-all verifier. There is no silent unauthenticated
// path on the chain-authority endpoint.
func capabilityVerifier() (mw.Verifier, error) {
	if os.Getenv("AUDIT_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: AUDIT_DEV_INSECURE_AUTH=1 — using dev allow-all capability verifier; NOT for production (real PASETO verifier lands in T-008)")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: packages/go/auth PASETO verifier lands in T-008 (ADR-0046); set AUDIT_DEV_INSECURE_AUTH=1 only for local dev")
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "service-service"}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
