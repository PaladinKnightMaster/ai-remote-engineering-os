package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/setup"
	"aireos.local/services/api/internal/store"
)

// runSetupCode handles `api setup-code issue [--ca-root <path>]` (ADR-0078 §D1): root on
// the server mints the one-time first-run setup code. stdout carries the code ALONE on
// its own line (so setup.sh can capture it) and, with --ca-root, one further line
// `ca-sha256: <hex>`. Nothing else -- and no log line -- ever carries the code.
func runSetupCode(dsn string, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "issue" {
		return errors.New("usage: api setup-code issue [--ca-root <path>]")
	}
	fs := flag.NewFlagSet("setup-code issue", flag.ContinueOnError)
	caRoot := fs.String("ca-root", "", "also print the SHA-256 fingerprint of this CA certificate (PEM or DER)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	// The environment operator always wins (ADR-0078 §D4): with one set, setup mode is
	// never entered, so a code would be dead on arrival.
	if _, err := account.FromEnv(); err == nil {
		return setup.ErrOperatorExists
	}
	fingerprint := ""
	if *caRoot != "" { // fail before issuing anything
		fp, err := caFingerprint(*caRoot)
		if err != nil {
			return err
		}
		fingerprint = fp
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	code, err := setup.NewStore(pool, store.OutboxTable).Issue(context.Background())
	if err != nil {
		return err
	}
	// A code that cannot be shown is a code nobody holds: fail rather than succeed silently.
	if _, err := fmt.Fprintln(stdout, code); err != nil {
		return fmt.Errorf("print setup code: %w", err)
	}
	if fingerprint != "" {
		if _, err := fmt.Fprintf(stdout, "ca-sha256: %s\n", fingerprint); err != nil {
			return fmt.Errorf("print CA fingerprint: %w", err)
		}
	}
	return nil
}

// caFingerprint is the lower-case hex SHA-256 of the certificate's DER bytes, so
// setup.sh needs no openssl on a minimal host (ADR-0078 §D7).
func caFingerprint(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read --ca-root: %w", err)
	}
	der := raw
	if block, _ := pem.Decode(raw); block != nil {
		der = block.Bytes
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		return "", fmt.Errorf("--ca-root %s is not a certificate: %w", path, err)
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}
