package migrate

import (
	"flag"
	"fmt"
	"strconv"
)

// RunSubcommand dispatches `<service> migrate <action> [args]`, the uniform
// subcommand surface every stateful service exposes from main.go (ADR-0044).
// args are the tokens after "migrate" (e.g. ["up"], ["force", "1"]). Migrations
// run as a deliberate operator action, never on service boot.
//
// audit-service is forward-only by policy (ADR-0044): no "down" action is
// offered here, so an operator cannot run a destructive rollback against the
// chained-hash evidence table even by mistake.
func RunSubcommand(r Runner, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("migrate: action required (up|version|force <v>)")
	}
	switch rest[0] {
	case "up":
		return r.Up()
	case "version":
		v, dirty, err := r.Version()
		if err != nil {
			return err
		}
		fmt.Printf("%s schema version: %d (dirty=%t)\n", r.Service, v, dirty)
		return nil
	case "force":
		if len(rest) < 2 {
			return fmt.Errorf("migrate force: version argument required")
		}
		v, err := strconv.Atoi(rest[1])
		if err != nil {
			return fmt.Errorf("migrate force: invalid version %q: %w", rest[1], err)
		}
		return r.Force(v)
	default:
		return fmt.Errorf("migrate: unknown action %q (want up|version|force)", rest[0])
	}
}
