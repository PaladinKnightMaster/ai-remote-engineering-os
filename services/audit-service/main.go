// Command audit-service is the append-only audit chain authority (ADR-0045).
//
// Subcommands (ADR-0044, ADR-0069):
//
//	audit-service serve                 # default; run the HTTP server
//	audit-service migrate up|version|force <v>
//	audit-service verify                # recompute + verify the chained-hash chain
//	audit-service checkpoint            # force one signed chain head now and exit (D3)
//	audit-service export                # stream the NDJSON evidence export to stdout (D3/D5/D6/D8)
package main

import (
	"context"
	"crypto/ed25519"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/migrate"
	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/checkpoint"
	"aireos.local/services/audit-service/internal/export"
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
	if cmd == "verify" {
		return runVerify(dsn)
	}
	if cmd == "checkpoint" {
		return runCheckpoint(dsn)
	}
	if cmd == "export" {
		return runExport(dsn)
	}
	if cmd != "serve" {
		return fmt.Errorf("unknown command %q (want serve|migrate|verify|checkpoint|export)", cmd)
	}
	return serve(dsn)
}

// runVerify reads the whole chain in order and checks its integrity with
// chain.Verify (recomputes every chainHash + checks each prevHash links to its
// predecessor). The operator-facing chain-integrity CLI, no external script
// (v0.1.0 release-criteria WD-1 step 9). Exits non-zero on a broken link.
func runVerify(dsn string) error {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	st := store.New(pool)

	const page = 200
	var all []audit.Event
	var after int64
	for {
		// Behavior-preserving (T-105 is store-layer-only): ascending,
		// unfiltered, no preset — the chain-order walk this CLI has always done.
		batch, last, err := st.List(ctx, store.ListQuery{
			Direction: store.Asc,
			Cursor:    after,
			Limit:     page,
		})
		if err != nil {
			return fmt.Errorf("list events: %w", err)
		}
		all = append(all, batch...)
		if len(batch) < page {
			break
		}
		after = last
	}
	if idx := chain.Verify(all); idx >= 0 {
		return fmt.Errorf("audit chain integrity BROKEN at index %d (seq=%d id=%s)", idx, all[idx].Seq, all[idx].ID)
	}
	fmt.Printf("audit chain intact: %d event(s) verified\n", len(all))
	return nil
}

// runCheckpoint forces ONE chain-head checkpoint attempt right now and exits
// — ADR-0069 D3's explicit operator action. The export path (T-125/T-126)
// must never sign on its own, so this is how an operator attests on demand
// between cadence ticks. Reports exactly which of the three outcomes
// happened; never silently does nothing without saying so.
func runCheckpoint(dsn string) error {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	signKey, err := loadChainSignKey()
	if err != nil {
		return err
	}
	if signKey == nil {
		return errors.New("AIREOS_AUDIT_CHAIN_SIGN_KEY is not set (expected in /etc/aireos/audit.env, ADR-0069 D5) — cannot checkpoint without a chain-head signing key")
	}

	sw := &checkpoint.Sweeper{Store: store.New(pool), Key: signKey, Anchor: anchorInterface(loadAnchorWriter())}
	out, err := sw.Checkpoint(ctx)
	if err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	switch {
	case out.LockHeld:
		fmt.Println("checkpoint: lock held by another writer, skipped")
	case out.Signed:
		fmt.Printf("checkpoint: signed chain head at seq %d\n", out.Seq)
	default:
		fmt.Println("checkpoint: nothing to do (no new events since the last head)")
	}
	return nil
}

// runExport streams the self-contained NDJSON evidence export to stdout
// (ADR-0069 D3/D5/D6/D8, T-125c) so it pipes and redirects naturally
// (`audit-service export > evidence.ndjson`). Follows runVerify's structure:
// its own pgxpool, its own unbounded context, exits non-zero on error.
// Deliberately never touches AIREOS_AUDIT_CHAIN_SIGN_KEY — export has no
// signing key at all (D3) — and reads the anchor purely for its own section
// (e), degrading gracefully to "no anchor available" when
// AIREOS_AUDIT_ANCHOR_PATH is unset or the file is absent, per
// internal/export's documented contract.
func runExport(dsn string) error {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	ex := &export.Exporter{
		Store:  store.New(pool),
		Anchor: exportAnchorReader(loadAnchorWriter()),
	}
	if err := ex.Export(ctx, os.Stdout); err != nil {
		return fmt.Errorf("export: %w", err)
	}
	return nil
}

// serve runs the HTTP server plus the ADR-0069 D2 cadence sweep, with
// graceful shutdown on SIGINT/SIGTERM (D2 also requires signing on shutdown
// — audit-service had no signal handling at all before this ticket).
func serve(dsn string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	v, err := capabilityVerifier()
	if err != nil {
		return err
	}

	st := store.New(pool)
	h := server.New(st, v)
	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("AUDIT_ADDR"), ":8081"),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	signKey, err := loadChainSignKey()
	if err != nil {
		return err
	}
	insecure := os.Getenv("AUDIT_DEV_INSECURE_AUTH") == "1"
	if signKey == nil {
		// T-125a flip (ADR-0069 D5): an absent key is FATAL — running unsigned
		// would make the audit chain ordering-only rather than authenticated,
		// and that must never happen silently. The one exception is the same
		// local-dev escape capabilityVerifier() already grants a missing
		// AIREOS_PASETO_KEY: AUDIT_DEV_INSECURE_AUTH=1 lets `go run
		// ./services/audit-service serve` keep working with no key material
		// at all, loudly, once, at boot.
		if !insecure {
			return fmt.Errorf("AIREOS_AUDIT_CHAIN_SIGN_KEY is not set (expected in /etc/aireos/audit.env, ADR-0069 D5) — refusing to start: running unsigned would make the audit chain ordering-only, not authenticated. Set AUDIT_DEV_INSECURE_AUTH=1 to run unsigned for local development only")
		}
		log.Print(devInsecureUnsignedBootWarning)
	}

	// ADR-0069 §4: resolve the anchor and re-sync it against the database
	// BEFORE the cadence sweep starts — an ordinary "one head signed since
	// the last graceful shutdown" gap must be closed here, so it is never
	// later confused with a tamper finding once the sweep is running.
	aw := loadAnchorWriter()
	resyncAnchorAtStartup(ctx, st, aw)

	sweeper := &checkpoint.Sweeper{
		Store:    st,
		Key:      signKey,
		Anchor:   anchorInterface(aw),
		Events:   checkpointEvents(),
		Interval: checkpointInterval(),
	}
	go sweeper.Run(ctx)

	serveErrc := make(chan error, 1)
	go func() {
		log.Printf("audit-service listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrc <- err
			return
		}
		serveErrc <- nil
	}()

	select {
	case err := <-serveErrc:
		return err
	case <-ctx.Done():
	}

	log.Print("audit-service: shutdown signal received, draining")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("audit-service: graceful HTTP shutdown error (continuing to exit): %v", err)
	}

	// Best-effort final checkpoint (D2). Its own short bounded context: this
	// MUST NOT be able to hang shutdown past systemd's TimeoutStopSec — a
	// blocked SIGTERM becomes a SIGKILL, which is strictly worse than simply
	// leaving the tail unattested (the verifier already reports that
	// honestly as INTACT+UNATTESTED). Log and exit cleanly on any outcome.
	if signKey != nil {
		finalCtx, cancelFinal := context.WithTimeout(context.Background(), 5*time.Second)
		out, ferr := sweeper.Checkpoint(finalCtx)
		cancelFinal()
		switch {
		case ferr != nil:
			log.Printf("audit-service: final checkpoint failed, exiting anyway: %v", ferr)
		case out.Signed:
			log.Printf("audit-service: final checkpoint signed chain head at seq %d", out.Seq)
		case out.LockHeld:
			log.Print("audit-service: final checkpoint skipped, lock held by another writer")
		default:
			log.Print("audit-service: final checkpoint had nothing new to sign")
		}
	}
	return nil
}

const devInsecureUnsignedBootWarning = "WARNING: AIREOS_AUDIT_CHAIN_SIGN_KEY is not set and AUDIT_DEV_INSECURE_AUTH=1 — " +
	"running UNSIGNED (chain-head signing disabled); the audit chain is ordering-only, not authenticated to a " +
	"third party (ADR-0069). This escape is for local development only; install.sh always mints a key for a real install."

// loadChainSignKey resolves the Ed25519 chain-head signing key (ADR-0069 D5).
//   - Unset -> (nil, nil): the caller decides fatal-vs-dev-escape (serve()) —
//     this function only parses, it never applies boot policy.
//   - Set but malformed -> a non-nil error: a corrupt key is an operator
//     error, not an absent one, and must fail loud at boot regardless of the
//     dev escape — same posture as capabilityVerifier()'s handling of a bad
//     AIREOS_PASETO_KEY.
func loadChainSignKey() (ed25519.PrivateKey, error) {
	keyHex := os.Getenv("AIREOS_AUDIT_CHAIN_SIGN_KEY")
	if keyHex == "" {
		return nil, nil
	}
	sk, err := chain.SecretKeyFromHex(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid AIREOS_AUDIT_CHAIN_SIGN_KEY: %w", err)
	}
	return sk, nil
}

// resyncTimeout bounds the whole ADR-0069 §4 startup re-sync (below) so a
// very large catch-up gap or a stuck filesystem cannot hang serve() from
// ever starting. Case (c3) — the truncation signature — never loops at all
// (it only logs; it never writes), so this bound only ever matters for the
// two self-healing cases, and PERF-15's own bound on how large
// audit_chain_heads can realistically get keeps a legitimate catch-up well
// inside it in practice.
const resyncTimeout = 30 * time.Second

// loadAnchorWriter resolves the ADR-0069 D4 out-of-Postgres anchor from
// AIREOS_AUDIT_ANCHOR_PATH. Returns nil when unset — anchoring is then
// disabled entirely. install.sh always sets this for a real install
// (T-125a); the compose/e2e stack sets it too (docker-compose.e2e.yml).
func loadAnchorWriter() *anchor.Writer {
	path := os.Getenv("AIREOS_AUDIT_ANCHOR_PATH")
	if path == "" {
		return nil
	}
	return anchor.New(path)
}

// anchorInterface converts a possibly-nil *anchor.Writer to the
// checkpoint.Anchor interface, preserving a TRUE nil interface when aw is
// nil. Assigning a nil *anchor.Writer directly to an interface-typed field
// would instead produce a non-nil interface holding a nil pointer (the
// classic Go interface-nil trap) — checkpoint.Sweeper's `s.Anchor == nil`
// check would then fail to catch it, panicking on the first Append call.
func anchorInterface(aw *anchor.Writer) checkpoint.Anchor {
	if aw == nil {
		return nil
	}
	return aw
}

// exportAnchorReader converts a possibly-nil *anchor.Writer to the
// export.AnchorReader interface, preserving a TRUE nil interface when aw is
// nil (same Go interface-nil trap anchorInterface above guards against —
// see its doc comment).
func exportAnchorReader(aw *anchor.Writer) export.AnchorReader {
	if aw == nil {
		return nil
	}
	return aw
}

// resyncAnchorAtStartup runs the ADR-0069 §1(c)/§4 startup re-sync: compare
// the anchor file's last recorded position against the database's last
// signed head, classify the three-way divergence (anchor.Classify), and
// self-heal the two benign cases by appending the missing heads. Runs
// exactly once, before sweeper.Run starts (serve(), above).
//
// Anchor errors here are FAIL-LOUD, NOT FAIL-CLOSED, same posture as the
// sweep itself (D10): every branch logs and returns, never blocks serve()
// from starting — a degraded truncation detector must never take the audit
// chain itself down.
func resyncAnchorAtStartup(ctx context.Context, st *store.Store, aw *anchor.Writer) {
	if aw == nil {
		log.Print("audit-service: AIREOS_AUDIT_ANCHOR_PATH not set — the out-of-Postgres truncation detector is DISABLED (ADR-0069 D4); a database restore or rewrite would be undetectable to a third party holding only an export")
		return
	}

	rctx, cancel := context.WithTimeout(ctx, resyncTimeout)
	defer cancel()

	dbHead, dbOk, err := st.LastHead(rctx)
	if err != nil {
		log.Printf("audit-service: startup anchor re-sync: read last DB head: %v (continuing to start; anchoring resumes on the next checkpoint)", err)
		return
	}

	last, anchorOk, err := aw.Last()
	if err != nil {
		log.Printf("audit-service: startup anchor re-sync: read anchor file %s: %v (continuing to start)", aw.Path(), err)
		return
	}

	if !anchorOk {
		// Missing/empty anchor file (ADR-0069 §1(d)): recreate with an
		// initialization marker documenting the gap. NEVER silently rebuild
		// the missing history from the database — an attacker who deletes
		// BOTH the anchor and the events it would have covered must not end
		// up with a "reconstructed" anchor that agrees with the tampered
		// chain by construction.
		seq := int64(0)
		if dbOk {
			seq = dbHead.Seq
		}
		if err := aw.WriteInitMarker(seq, "anchor file absent at startup; ADR-0069 D4 forbids reconstructing prior history from Postgres"); err != nil {
			log.Printf("audit-service: startup anchor re-sync: failed to write init marker: %v (continuing to start; the truncation detector remains degraded)", err)
			return
		}
		log.Printf("audit-service: anchor file initialized at seq %d with no prior history — everything before this point is NOT independently anchored", seq)
		return
	}

	if !dbOk {
		// The anchor has recorded head(s) but the database has NONE at all —
		// the anchor is unconditionally ahead, the truncation signature (or
		// a legitimate restore from an older/empty backup, which produces
		// the identical signature — see DivergenceTruncation's case below
		// for why this service still starts rather than refusing to boot).
		log.Printf("TRUNCATION SIGNATURE: audit-service: anchor %s holds head(s) up to seq %d but the database has NO chain heads at all — the anchor file is left untouched; a verifier (T-126) is the authority on tampering, not this service", aw.Path(), last.Seq)
		return
	}

	switch anchor.Classify(last.Seq, dbHead.Seq) {
	case anchor.DivergenceNone:
		log.Printf("audit-service: anchor in sync with the database at seq %d", last.Seq)
	case anchor.DivergenceBenignCrash:
		log.Printf("audit-service: anchor behind the database by 1 (anchor=%d db=%d) — benign crash window, self-healing", last.Seq, dbHead.Seq)
		selfHealAnchor(rctx, st, aw, last.Seq)
	case anchor.DivergenceWriterBroken:
		log.Printf("OPERATIONAL ALARM: audit-service: anchor is behind the database by %d heads (anchor=%d db=%d) — the anchor WRITER appears broken (check disk space and permissions at %s); this is NOT a tamper finding; self-healing by appending the missing heads now",
			dbHead.Seq-last.Seq, last.Seq, dbHead.Seq, aw.Path())
		selfHealAnchor(rctx, st, aw, last.Seq)
	case anchor.DivergenceTruncation:
		// The anchor is AHEAD of the database — the truncation signature.
		// Never touch the anchor to "fix" this (the divergence itself is
		// the evidence); never refuse to start (a legitimate restore from
		// an older backup produces the identical signature, and bricking a
		// stack on routine recovery makes nothing more verifiable — the
		// verifier, not this service, is the authority on tampering).
		log.Printf("TRUNCATION SIGNATURE: audit-service: anchor at seq %d is AHEAD of the database's last head at seq %d — this can mean the audit chain was truncated below the anchor, or a legitimate restore from an older backup; the anchor file is left untouched; continuing startup", last.Seq, dbHead.Seq)
	}
}

// selfHealAnchor appends every DB head with seq > since to the anchor,
// closing the DivergenceBenignCrash / DivergenceWriterBroken cases. Stops on
// the first Append error (fail-loud, not a retry loop) and always logs how
// many it appended.
func selfHealAnchor(ctx context.Context, st *store.Store, aw *anchor.Writer, since int64) {
	heads, err := st.HeadsSince(ctx, since)
	if err != nil {
		log.Printf("audit-service: startup anchor re-sync: read heads since seq %d: %v", since, err)
		return
	}
	appended := 0
	for _, h := range heads {
		if err := aw.Append(h); err != nil {
			log.Printf("audit-service: startup anchor re-sync: append seq %d failed: %v (stopping self-heal; %d appended so far)", h.Seq, err, appended)
			return
		}
		appended++
	}
	log.Printf("audit-service: startup anchor re-sync appended %d missing head(s)", appended)
}

// checkpointEvents / checkpointInterval resolve the D2 cadence overrides.
// Both fall back to checkpoint.DefaultEvents/DefaultInterval on an unset or
// invalid value (logged, never fatal — a malformed cadence override is an
// operator typo, not a reason to refuse to serve).
func checkpointEvents() int64 {
	v := os.Getenv("AIREOS_AUDIT_CHECKPOINT_EVENTS")
	if v == "" {
		return checkpoint.DefaultEvents
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		log.Printf("audit-service: invalid AIREOS_AUDIT_CHECKPOINT_EVENTS=%q, using default %d", v, checkpoint.DefaultEvents)
		return checkpoint.DefaultEvents
	}
	return n
}

func checkpointInterval() time.Duration {
	v := os.Getenv("AIREOS_AUDIT_CHECKPOINT_INTERVAL")
	if v == "" {
		return checkpoint.DefaultInterval
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("audit-service: invalid AIREOS_AUDIT_CHECKPOINT_INTERVAL=%q, using default %s", v, checkpoint.DefaultInterval)
		return checkpoint.DefaultInterval
	}
	return d
}

// capabilityVerifier resolves the capability Verifier (ADR-0046).
//
//   - AIREOS_PASETO_KEY (hex cluster key) set → real PASETO v4 verifier from
//     packages/go/auth, requiring audience "audit-service".
//   - else AUDIT_DEV_INSECURE_AUTH=1 → dev-only allow-all verifier.
//   - else FAIL LOUD: there is no silent unauthenticated path on the
//     chain-authority endpoint.
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID") // optional; empty skips the issuer rule
		local := auth.NewVerifier(key, issuer, "audit-service")
		// ADR-0060: also accept the gateway's asymmetric outbox token (the gateway
		// drops the cluster key in T-049). Dual-mode: every other producer (api/broker/
		// connector/policy) stays symmetric, so audit is a permanent dual-mode boundary.
		if pubHex := os.Getenv("AIREOS_GATEWAY_PUBLIC_KEY"); pubHex != "" {
			pub, perr := auth.NewAsymmetricPublicKeyFromHex(pubHex)
			if perr != nil {
				return nil, fmt.Errorf("invalid AIREOS_GATEWAY_PUBLIC_KEY: %w", perr)
			}
			gw := auth.NewAsymmetricVerifier(pub, issuer, "audit-service")
			return auth.AsMiddlewareVerifier(auth.NewDualModeVerifier(local, gw)), nil
		}
		return auth.AsMiddlewareVerifier(local), nil
	}
	if os.Getenv("AUDIT_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: AUDIT_DEV_INSECURE_AUTH=1 — using dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY (hex cluster key, ADR-0046) or AUDIT_DEV_INSECURE_AUTH=1 for local dev")
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
