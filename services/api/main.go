// Command api is the operator-facing gateway (ADR-0043). T-005a stands up the
// skeleton + the resource-registry store/migration; the resource HTTP surface
// is wired in T-005b, and operator login (ADR-0046) in the auth ticket.
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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/migrate"
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

	r := chi.NewRouter()
	// Readiness only at T-005a; the resource routes (gated by RequireCapability)
	// land in T-005b.
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		if err := pool.Ping(req.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("API_ADDR"), ":8080"),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("api listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
