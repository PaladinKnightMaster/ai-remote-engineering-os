// Command connector-service is the credential-custody + egress gateway
// (ADR-0053). At v0.2.0 it becomes the SOLE holder of AIREOS_RESOURCE_SECRET_KEY
// and the only service that runs pgp_sym_encrypt/decrypt on target credentials,
// closing the v0.1.0 SEC-12 pgcrypto debt (services/api and the broker lose
// their decrypt paths in T-030).
//
// T-029a landed the persistence spine (credential table + custody store +
// provisioning/relocation CLIs). T-029b-i (this slice) adds the transactional
// outbox + the HTTP `serve` surface (ungated /healthz + the in-process audit
// relay). The gated credential-lease RPC + egress-decision audit are T-029b-ii;
// the consumer re-point (removing api/broker decrypt, dropping
// resources.connection_secret) is T-030.
//
// Subcommands:
//
//	connector-service serve                 # default; HTTP surface + in-process outbox relay
//	connector-service migrate up|version|force <v>
//	connector-service outbox relay          # run the outbox relay in the foreground
//	connector-service credentials genkey    # print a fresh AIREOS_RESOURCE_SECRET_KEY (no DB)
//	connector-service credentials set-secret <resource-id> --key-file <path> [--passphrase <pp>]
//	connector-service credentials unset-secret <resource-id>   # remove a stored credential (T-118b --undo)
//	connector-service credentials migrate-legacy   # backfill ciphertext from resources.connection_secret
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
	"aireos.local/packages/go/outbox"
	"aireos.local/services/connector-service/internal/server"
	"aireos.local/services/connector-service/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("connector-service: %v", err)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	// `credentials genkey` is offline (no DB) — it generates the resource secret
	// key (relocated here from `api resources genkey`, T-030: connector-service is
	// the sole key holder). Handle it before resolving the DSN so it works during
	// provisioning, before a DB exists.
	if cmd == "credentials" && len(args) > 1 && args[1] == "genkey" {
		_, hexKey := auth.GenerateKey()
		fmt.Println(hexKey)
		return nil
	}
	dsn := firstNonEmpty(os.Getenv("CONNECTOR_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("CONNECTOR_DATABASE_URL (or DATABASE_URL) must be set")
	}
	switch cmd {
	case "serve":
		return serve(dsn)
	case "migrate":
		return migrate.RunSubcommand(migrate.Runner{
			Service: "connector-service",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	case "outbox":
		return runOutbox(dsn, args[1:])
	case "credentials":
		return runCredentials(dsn, args[1:])
	default:
		return fmt.Errorf("unknown command %q (want serve|migrate|outbox|credentials)", cmd)
	}
}

// serve runs the HTTP surface (ungated /healthz at T-029b-i; the gated lease RPC
// lands in T-029b-ii) and starts the in-process outbox relay (ADR-0045).
// Best-effort relay: if AIREOS_AUDIT_URL is unset the relay is skipped and
// connector still serves — a degraded chain authority surfaces as growing outbox
// depth, not a failed boot.
func serve(dsn string) error {
	if err := checkKeySeparation(); err != nil {
		return err
	}
	v, err := capabilityVerifier()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if relay, err := newRelay(pool); err != nil {
		log.Printf("outbox relay disabled: %v", err)
	} else {
		go func() {
			if err := relay.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("outbox relay stopped: %v", err)
			}
		}()
	}

	// The lease handler decrypts target credentials with AIREOS_RESOURCE_SECRET_KEY.
	// Empty is tolerated at boot — the lease answers 503 until provisioned — so
	// connector can run (healthz, relay) before any target secret exists.
	secretKey := os.Getenv("AIREOS_RESOURCE_SECRET_KEY")
	if secretKey == "" {
		log.Print("AIREOS_RESOURCE_SECRET_KEY not set — credential lease will answer 503 until provisioned")
	}

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("CONNECTOR_ADDR"), ":8083"),
		Handler:           server.New(server.Config{Pool: pool, Verifier: v, SecretKey: secretKey}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("connector-service listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// capabilityVerifier verifies the broker's service-service token. The broker
// mints IssueService("session-broker","connector-service"), so the verified
// audience is "connector-service". Mirrors the other services' real-PASETO-or-
// dev-opt-in shape.
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, err
		}
		return auth.AsMiddlewareVerifier(auth.NewVerifier(key, os.Getenv("AIREOS_CLUSTER_ID"), "connector-service")), nil
	}
	if os.Getenv("CONNECTOR_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: CONNECTOR_DEV_INSECURE_AUTH=1 — dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY or CONNECTOR_DEV_INSECURE_AUTH=1 for local dev")
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "service-service"}, nil
}

// checkKeySeparation fails loud when the resource secret key collides with the
// PASETO signing key (authority separation, ADR-0048 §7 / ADR-0053). No-op when
// either is unset.
func checkKeySeparation() error {
	rk := os.Getenv("AIREOS_RESOURCE_SECRET_KEY")
	pk := os.Getenv("AIREOS_PASETO_KEY")
	if rk != "" && pk != "" && rk == pk {
		return errors.New("AIREOS_RESOURCE_SECRET_KEY must differ from AIREOS_PASETO_KEY (authority separation, ADR-0048 §7)")
	}
	return nil
}

// runOutbox handles `connector-service outbox relay` — the operator-run,
// foreground relay, the same shape as the goroutine serve starts (ADR-0045).
func runOutbox(dsn string, args []string) error {
	if len(args) == 0 || args[0] != "relay" {
		return errors.New("usage: connector-service outbox relay")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	relay, err := newRelay(pool)
	if err != nil {
		return err
	}
	log.Print("connector-service: outbox relay (foreground)")
	return relay.Run(context.Background())
}

// newRelay builds the outbox relay draining connector_outbox into audit-service.
// AIREOS_AUDIT_URL must be set or the relay is declined (logged, not fatal). The
// bearer is a service-service PASETO token when AIREOS_PASETO_KEY is set
// (ADR-0046); else CONNECTOR_AUDIT_TOKEN (dev), else none.
func newRelay(pool *pgxpool.Pool) (*outbox.Relay, error) {
	auditURL := os.Getenv("AIREOS_AUDIT_URL")
	if auditURL == "" {
		return nil, errors.New("AIREOS_AUDIT_URL not set")
	}
	token, err := auditTokenSource()
	if err != nil {
		return nil, err
	}
	return &outbox.Relay{
		Pool:    pool,
		Client:  &outbox.HTTPClient{BaseURL: auditURL, Token: token},
		Table:   "connector_outbox",
		Channel: "connector_outbox_new",
	}, nil
}

// auditTokenSource returns the bearer-token provider for audit-service ingest.
func auditTokenSource() (func(context.Context) (string, error), error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		signer := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID"))
		return func(context.Context) (string, error) {
			return signer.IssueService("connector-service", "audit-service")
		}, nil
	}
	staticTok := os.Getenv("CONNECTOR_AUDIT_TOKEN")
	return func(context.Context) (string, error) { return staticTok, nil }, nil
}

// runCredentials handles `connector-service credentials <set-secret|unset-secret|migrate-legacy>`.
func runCredentials(dsn string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: connector-service credentials genkey | set-secret <resource-id> --key-file <path> [--passphrase <pp>] | unset-secret <resource-id> | migrate-legacy")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	ctx := context.Background()

	switch args[0] {
	case "set-secret":
		return setSecret(ctx, pool, args[1:])
	case "unset-secret":
		return unsetSecret(ctx, pool, args[1:])
	case "migrate-legacy":
		n, err := store.BackfillFromResources(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "connector-service: backfilled %d credential(s) from resources.connection_secret\n", n)
		return nil
	default:
		return fmt.Errorf("unknown credentials subcommand %q (want set-secret|unset-secret|migrate-legacy)", args[0])
	}
}

// unsetSecret removes a resource's stored credential (T-118b --undo). Naturally
// idempotent (store.Delete is an unconditional DELETE) — a repeat call against an
// already-removed or never-bound resource-id succeeds silently.
func unsetSecret(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	if len(args) == 0 {
		return errors.New("unset-secret requires a <resource-id>")
	}
	if err := store.Delete(ctx, pool, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "connector-service: removed credential for resource %s (if any)\n", args[0])
	return nil
}

// setSecret pgcrypto-encrypts a target SSH private key onto the resource's
// connector-custodied credential row (the ADR-0053 §5 re-point of provisioning
// out of `api resources set-secret`).
func setSecret(ctx context.Context, pool *pgxpool.Pool, args []string) error {
	if len(args) == 0 {
		return errors.New("set-secret requires a <resource-id>")
	}
	id := args[0]
	fs := flag.NewFlagSet("set-secret", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "path to the target SSH private key (PEM) file")
	passphrase := fs.String("passphrase", "", "optional private-key passphrase")
	if err := fs.Parse(args[1:]); err != nil {
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
	if err := store.Set(ctx, pool, id, key, store.Material{PrivateKey: string(pem), Passphrase: *passphrase}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "connector-service: stored credential for resource %s\n", id)
	return nil
}

// resourceSecretKey resolves AIREOS_RESOURCE_SECRET_KEY and enforces authority
// separation from the PASETO signing key (ADR-0048 §7 / ADR-0053). The key
// relocates from services/api's env to connector-service's at v0.2.0.
func resourceSecretKey() (string, error) {
	key := os.Getenv("AIREOS_RESOURCE_SECRET_KEY")
	if key == "" {
		return "", errors.New("AIREOS_RESOURCE_SECRET_KEY must be set (generate one with `connector-service credentials genkey`)")
	}
	if pk := os.Getenv("AIREOS_PASETO_KEY"); pk != "" && pk == key {
		return "", errors.New("AIREOS_RESOURCE_SECRET_KEY must differ from AIREOS_PASETO_KEY (authority separation, ADR-0048 §7)")
	}
	return key, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
