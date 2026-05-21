// Package migrate is the shared golang-migrate v4 library-mode runner consumed
// identically by every stateful P0 service (ADR-0044). Each service embeds its
// own migrations/*.sql via embed.FS and hands the FS to a Runner; no external
// migration CLI ships in the deploy artifact.
package migrate

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // registers the postgres database driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// ApplyEvent describes a single applied migration step, handed to a Runner's
// optional OnApply hook so a service can emit a structured audit event when its
// schema changes (ADR-0044: migration application is a capability-edge event).
// For audit-service this is the in-process append into its own chained-hash log;
// the wiring of that hook is a documented P0 gap (ADR-0044 §Verification).
type ApplyEvent struct {
	Service string
	Version uint
	Dirty   bool
}

// Runner applies a service's embedded migrations against a Postgres DSN.
type Runner struct {
	// Service is the owning service name (used in audit emission + advisory-lock
	// namespacing by golang-migrate's postgres driver).
	Service string
	// FS holds the embedded migrations tree, rooted at Dir.
	FS embed.FS
	// Dir is the path within FS that contains the NNNN_*.sql files.
	Dir string
	// DSN is the Postgres connection string.
	DSN string
	// OnApply, if set, is called once per applied step after a successful Up.
	OnApply func(ApplyEvent)
}

func (r Runner) instance() (*migrate.Migrate, error) {
	src, err := iofs.New(r.FS, r.Dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: load embedded source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, r.DSN)
	if err != nil {
		return nil, fmt.Errorf("migrate: open %s: %w", r.Service, err)
	}
	return m, nil
}

// Up applies all pending migrations. A no-op (migrate.ErrNoChange) is not an
// error. On success it reports the resulting version through OnApply.
func (r Runner) Up() error {
	m, err := r.instance()
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up %s: %w", r.Service, err)
	}
	if r.OnApply != nil {
		v, dirty, verr := m.Version()
		if verr == nil {
			r.OnApply(ApplyEvent{Service: r.Service, Version: v, Dirty: dirty})
		}
	}
	return nil
}

// Version returns the current schema version and dirty flag.
func (r Runner) Version() (version uint, dirty bool, err error) {
	m, err := r.instance()
	if err != nil {
		return 0, false, err
	}
	defer m.Close()
	return m.Version()
}

// ErrNoChange is re-exported so callers can distinguish a clean no-op Up from a
// real failure without importing golang-migrate directly.
var ErrNoChange = migrate.ErrNoChange

// Force sets the schema version without running migrations, clearing the dirty
// flag — the documented recovery path for a failed migration (ADR-0044: the
// service subcommand is the audited entry point, not the migrate CLI).
func (r Runner) Force(version int) error {
	m, err := r.instance()
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Force(version); err != nil {
		return fmt.Errorf("migrate force %s: %w", r.Service, err)
	}
	return nil
}
