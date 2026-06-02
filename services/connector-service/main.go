// Command connector-service is the credential-custody + egress gateway
// (ADR-0053). At v0.2.0 it becomes the SOLE holder of AIREOS_RESOURCE_SECRET_KEY
// and the only service that runs pgp_sym_encrypt/decrypt on target credentials,
// closing the v0.1.0 SEC-12 pgcrypto debt (services/api and the broker lose
// their decrypt paths in T-030).
//
// T-029a lands the persistence spine only — the credential table, the custody
// store, and the provisioning / relocation CLIs. The credential-lease RPC, the
// transactional outbox, and egress-decision audit are T-029b; the consumer
// re-point (removing api/broker decrypt, dropping resources.connection_secret)
// is T-030. There is deliberately no `serve` subcommand yet — connector exposes
// no HTTP surface until the lease RPC lands.
//
// Subcommands:
//
//	connector-service migrate up|version|force <v>
//	connector-service credentials set-secret <resource-id> --key-file <path> [--passphrase <pp>]
//	connector-service credentials migrate-legacy   # backfill ciphertext from resources.connection_secret
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/migrate"
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
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	dsn := firstNonEmpty(os.Getenv("CONNECTOR_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("CONNECTOR_DATABASE_URL (or DATABASE_URL) must be set")
	}
	switch cmd {
	case "migrate":
		return migrate.RunSubcommand(migrate.Runner{
			Service: "connector-service",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	case "credentials":
		return runCredentials(dsn, args[1:])
	default:
		return fmt.Errorf("unknown command %q (want migrate|credentials)", cmd)
	}
}

// runCredentials handles `connector-service credentials <set-secret|migrate-legacy>`.
func runCredentials(dsn string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: connector-service credentials set-secret <resource-id> --key-file <path> [--passphrase <pp>] | migrate-legacy")
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
	case "migrate-legacy":
		n, err := store.BackfillFromResources(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "connector-service: backfilled %d credential(s) from resources.connection_secret\n", n)
		return nil
	default:
		return fmt.Errorf("unknown credentials subcommand %q (want set-secret|migrate-legacy)", args[0])
	}
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
		return "", errors.New("AIREOS_RESOURCE_SECRET_KEY must be set (generate one with `api resources genkey`)")
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
