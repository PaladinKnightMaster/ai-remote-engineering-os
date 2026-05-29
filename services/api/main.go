// Command api is the operator-facing gateway (ADR-0043). It serves the resource
// registry (T-005b) gated by capability tokens; operator login (ADR-0046) lands
// in a later auth ticket.
//
// Subcommands:
//
//	api serve                       # default; run the HTTP server
//	api migrate up|version|force <v>
//	api resources genkey                                  # print a fresh AIREOS_RESOURCE_SECRET_KEY
//	api resources set-secret <id> --key-file <path> [--passphrase <pp>]
//
// (The shared migrate subcommand exposes up|version|force; wiring a `down`
// action for standard up/down services like api is a small follow-up on
// packages/go/migrate — the 0001 down.sql file already exists.)
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/migrate"
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/secret"
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
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	// `resources genkey` is offline (no DB) — mirror `authctl genkey`. Handle it
	// before resolving the DSN so it works during provisioning, before a DB exists.
	if cmd == "resources" && len(args) > 1 && args[1] == "genkey" {
		_, hexKey := auth.GenerateKey()
		fmt.Println(hexKey)
		return nil
	}
	dsn := firstNonEmpty(os.Getenv("API_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("API_DATABASE_URL (or DATABASE_URL) must be set")
	}
	switch cmd {
	case "migrate":
		return migrate.RunSubcommand(migrate.Runner{
			Service: "api",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	case "resources":
		return runResources(dsn, args[1:])
	case "serve":
		return serve(dsn)
	default:
		return fmt.Errorf("unknown command %q (want serve|migrate|resources)", cmd)
	}
}

// runResources handles the operator-side resource-secret provisioning surface
// (ADR-0048 §7). `genkey` is handled in run() (no DB); set-secret needs the DB.
func runResources(dsn string, args []string) error {
	if len(args) == 0 || args[0] != "set-secret" {
		return errors.New("usage: api resources set-secret <resource-id> --key-file <path> [--passphrase <pp>]")
	}
	if len(args) < 2 {
		return errors.New("set-secret requires a <resource-id>")
	}
	id := args[1]
	fs := flag.NewFlagSet("set-secret", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "path to the target SSH private key (PEM) file")
	passphrase := fs.String("passphrase", "", "optional private-key passphrase")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *keyFile == "" {
		return errors.New("--key-file is required")
	}
	key, err := resourceSecretKey()
	if err != nil {
		return err
	}
	pem, err := os.ReadFile(*keyFile)
	if err != nil {
		return fmt.Errorf("read key file: %w", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	m := secret.Material{PrivateKey: string(pem), Passphrase: *passphrase}
	if err := secret.Set(context.Background(), pool, id, key, m); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "api: connection secret set for resource %s\n", id)
	return nil
}

// resourceSecretKey resolves AIREOS_RESOURCE_SECRET_KEY and enforces authority
// separation from the PASETO signing key (ADR-0048 §7 / §SEC-12). Fails loud if
// the key is unset or collides with AIREOS_PASETO_KEY.
func resourceSecretKey() (string, error) {
	key := os.Getenv("AIREOS_RESOURCE_SECRET_KEY")
	if key == "" {
		return "", errors.New("AIREOS_RESOURCE_SECRET_KEY must be set (generate one with `api resources genkey`)")
	}
	if err := checkKeySeparation(); err != nil {
		return "", err
	}
	return key, nil
}

// checkKeySeparation fails loud when the resource secret key collides with the
// PASETO signing key (defense-in-depth, ADR-0048 §7 authority separation). It is
// a no-op when the resource key is unset (decryption is a slice-b concern; the
// server need not require the key to start at v0.1.0).
func checkKeySeparation() error {
	rk := os.Getenv("AIREOS_RESOURCE_SECRET_KEY")
	pk := os.Getenv("AIREOS_PASETO_KEY")
	if rk != "" && pk != "" && rk == pk {
		return errors.New("AIREOS_RESOURCE_SECRET_KEY must differ from AIREOS_PASETO_KEY (authority separation, ADR-0048 §7)")
	}
	return nil
}

func serve(dsn string) error {
	if err := checkKeySeparation(); err != nil {
		return err
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	v, signer, authn, err := buildAuth()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("API_ADDR"), ":8080"),
		Handler:           server.New(store.New(pool), authn, signer, v),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("api listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// buildAuth resolves the capability Verifier plus the Signer + operator
// Authenticator used by login (ADR-0046). With AIREOS_PASETO_KEY set: a real
// PASETO verifier (audience "api"), a Signer, and the env-provisioned bootstrap
// operator (login is disabled with a 503 if the operator env is absent). Else
// API_DEV_INSECURE_AUTH=1 gives the dev allow-all verifier (no signer/operator,
// so login is disabled). Else fail loud.
func buildAuth() (mw.Verifier, *auth.Signer, server.Authenticator, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID") // optional; empty skips the issuer rule
		v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, issuer, "api"))
		signer := auth.NewSigner(key, issuer)
		var authn server.Authenticator
		if op, err := account.FromEnv(); err == nil {
			authn = op
		} else {
			log.Printf("operator login disabled: %v", err)
		}
		return v, signer, authn, nil
	}
	if os.Getenv("API_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: API_DEV_INSECURE_AUTH=1 — using dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil, nil, nil
	}
	return nil, nil, nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY (hex cluster key, ADR-0046) or API_DEV_INSECURE_AUTH=1 for local dev")
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
