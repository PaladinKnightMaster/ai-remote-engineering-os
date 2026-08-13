// Command aireos-verify is the offline, third-party evidence verifier
// ADR-0069 D6 charters: a standalone binary that checks an evidence export
// (internal/export's NDJSON format) against a supplied public key, with NO
// database, NO network, and NO configuration file. It exists precisely so
// that the claim "an outsider can check us" is something a stranger can run
// — not something only our own services can attempt.
//
// It lives inside the audit-service module (services/audit-service/cmd/
// aireos-verify), not a separate module, so it can import
// internal/export, internal/verify, internal/anchor and internal/chain
// directly rather than restating any of their types or logic — see each
// package's own doc comment for why duplicating this logic is exactly the
// defect class this release exists to close.
//
// # SEC-44 — non-circularity
//
// This binary NEVER reads a key out of the export it is verifying. Keys
// come ONLY from --pubkey and --keyring. A trust root shipped inside the
// artifact it validates would be circular — an attacker who can forge the
// export could forge the key alongside it, and this binary would happily
// confirm its own forgery. internal/export's format never carries raw key
// material anyway (only the KeyIDFor fingerprint, per export.go's package
// doc "What this export discloses"), but this binary does not even look:
// it never touches ManifestRecord.ChainHeadKeyID or any head's KeyID field
// as a SOURCE of trust, only as a label to report which supplied key (if
// any) matched.
//
// # Two independent verdicts
//
// The chain axis (INTACT+ATTESTED / INTACT+UNATTESTED / BROKEN,
// internal/verify's D6 proof) and the anchor axis (IN_SYNC / BENIGN_LAG /
// WRITER_BROKEN / TRUNCATION_SIGNATURE / COVERAGE_GAP / ABSENT,
// internal/anchor.Classify) answer genuinely different questions — a chain
// can recompute perfectly from Genesis while the anchor shows events were
// deleted off the tail (ADR-0069 D4). Both are always reported, never
// collapsed into one bit.
//
// # Two bounded passes over a seekable source
//
// Step 1 of D6 needs every head's signature verified BEFORE step 2 (the
// event recompute) begins — but heads physically appear AFTER events in
// the export format. This binary therefore makes two passes over the same
// seekable input (a real file, or stdin buffered once to a temp file so it
// becomes seekable): pass 1 drains events (discarding them — this is what
// runs the manifest-vs-body strictness check) and collects the heads and
// anchor records (both bounded to ~1% of event volume by ADR-0069 D2/
// PERF-15, safe to hold fully); pass 2 re-reads from the start and streams
// events directly into internal/verify.Chain as its EventSource, so memory
// stays bounded by the running hash, not by the size of the export. This
// mirrors internal/export's own two-bounded-passes design on the write
// side (export.go's package doc (b)) — see internal/export/reader.go's
// package doc "Two bounded passes" for the same idiom stated there.
//
// # Exit codes (built for unattended, air-gapped scripting)
//
//	0   chain INTACT+ATTESTED AND anchor IN_SYNC — nothing else exits 0.
//	1   chain BROKEN — a forgery or a linkage failure.
//	2   anchor TRUNCATION_SIGNATURE — events appear deleted, even if the
//	    chain itself recomputes cleanly.
//	3   degraded/warning: INTACT+UNATTESTED, a coverage gap, WRITER_BROKEN,
//	    BENIGN_LAG, or anchor ABSENT.
//	64  usage error (bad flags, unreadable/malformed input) — EX_USAGE.
//
// Priority when more than one condition applies: 1 (BROKEN) beats 2
// (TRUNCATION_SIGNATURE) beats 0 (both clean) beats 3 (everything else).
package main

import (
	"bufio"
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/export"
	"aireos.local/services/audit-service/internal/verify"
)

// Exit codes — see the package doc's table. Named constants so the mapping
// logic below reads like the table it implements.
const (
	exitOK         = 0
	exitBroken     = 1
	exitTruncation = 2
	exitDegraded   = 3
	exitUsageError = 64
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aireos-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var pubkeys hexKeyList
	fs.Var(&pubkeys, "pubkey", "hex-encoded Ed25519 chain-head public key (repeatable — ADR-0069 D5: keys are additive-forever, supply every key this install has ever signed under)")
	var keyringPath string
	fs.StringVar(&keyringPath, "keyring", "", "path to a file of hex-encoded Ed25519 public keys, one per line, # comments allowed")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: aireos-verify [--pubkey <hex> ...] [--keyring <file>] <export-file>|-")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsageError
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "aireos-verify: expected exactly one export file argument (a path, or - for stdin)")
		fs.Usage()
		return exitUsageError
	}
	path := fs.Arg(0)

	keys, err := loadKeyring(pubkeys, keyringPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "aireos-verify: %v\n", err)
		return exitUsageError
	}

	code, err := verifyExport(path, keys, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "aireos-verify: %v\n", err)
	}
	return code
}

// hexKeyList is a repeatable flag.Value collecting every --pubkey given.
type hexKeyList []string

func (h *hexKeyList) String() string { return strings.Join(*h, ",") }
func (h *hexKeyList) Set(v string) error {
	*h = append(*h, v)
	return nil
}

// loadKeyring resolves every --pubkey plus every non-comment, non-blank
// line of --keyring (if given) into a verify.KeyRing. An empty result
// (zero keys supplied) is not itself an error here — Chain's own step 1
// will correctly report OutcomeBroken/UnknownKeyID against any head that
// exists, which is a legitimate finding, not a usage error.
func loadKeyring(pubkeyHexes []string, keyringPath string) (verify.KeyRing, error) {
	var pubs []ed25519.PublicKey
	for _, h := range pubkeyHexes {
		p, err := chain.PublicKeyFromHex(strings.TrimSpace(h))
		if err != nil {
			return nil, fmt.Errorf("--pubkey %q: %w", h, err)
		}
		pubs = append(pubs, p)
	}
	if keyringPath != "" {
		f, err := os.Open(keyringPath)
		if err != nil {
			return nil, fmt.Errorf("--keyring %s: %w", keyringPath, err)
		}
		defer func() { _ = f.Close() }()

		sc := bufio.NewScanner(f)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := sc.Text()
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = line[:i]
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			p, err := chain.PublicKeyFromHex(line)
			if err != nil {
				return nil, fmt.Errorf("--keyring %s line %d: %w", keyringPath, lineNo, err)
			}
			pubs = append(pubs, p)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("--keyring %s: %w", keyringPath, err)
		}
	}
	return verify.NewKeyRing(pubs...), nil
}

// verifyExport runs the whole two-pass verification described in the
// package doc and writes the human-readable report to out. The returned
// error is non-nil ONLY for the exitUsageError case (open/parse/seek
// failure) — every other exit code is a legitimate verdict, reported via
// the return code alone with no accompanying error.
func verifyExport(path string, keys verify.KeyRing, out io.Writer) (int, error) {
	f, cleanup, err := openSeekable(path)
	if err != nil {
		return exitUsageError, fmt.Errorf("open export: %w", err)
	}
	defer cleanup()

	// Pass 1 (metadata): drain events (discarding them — this is what runs
	// the eventCount strictness check), collect heads + anchor records.
	p1, err := export.NewParser(f)
	if err != nil {
		return exitUsageError, fmt.Errorf("parse export: %w", err)
	}
	manifest := p1.Manifest()
	headRecords, err := p1.Heads()
	if err != nil {
		return exitUsageError, fmt.Errorf("parse export: %w", err)
	}
	anchorRecordsRaw, err := p1.AnchorRecords()
	if err != nil {
		return exitUsageError, fmt.Errorf("parse export: %w", err)
	}

	heads := make([]chain.Head, len(headRecords))
	dbHeadSeqs := make([]int64, len(headRecords))
	for i, hr := range headRecords {
		h := hr.Head()
		heads[i] = h
		dbHeadSeqs[i] = h.Seq
	}
	anchorRecords := make([]anchor.Record, len(anchorRecordsRaw))
	for i, ar := range anchorRecordsRaw {
		anchorRecords[i] = ar.ToRecord()
	}

	// Pass 2 (streaming): re-seek to the start and stream events
	// incrementally into verify.Chain — the SAME two-step proof, the SAME
	// package, main.go's `audit-service verify` subcommand uses against a
	// live database.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return exitUsageError, fmt.Errorf("seek export for the streaming pass: %w", err)
	}
	p2, err := export.NewParser(f)
	if err != nil {
		return exitUsageError, fmt.Errorf("re-parse export for the streaming pass: %w", err)
	}
	result, verr := verify.Chain(p2, heads, keys)
	if verr != nil {
		return exitUsageError, fmt.Errorf("stream export: %w", verr)
	}

	// Anchor axis, ALWAYS reported independently of the chain axis (a chain
	// can recompute perfectly while the anchor shows deletion, ADR-0069 D4).
	var classification anchor.Classification
	anchorVerdict := "ABSENT"
	if manifest.AnchorAvailable {
		classification = anchor.Classify(anchorRecords, dbHeadSeqs)
		anchorVerdict = classifyLabel(classification)
	}

	_, _ = fmt.Fprintf(out, "aireos-verify: %s\n", path)
	_, _ = fmt.Fprintf(out, "chain:  %s\n", describeChain(result, len(keys)))
	_, _ = fmt.Fprintf(out, "anchor: %s\n", describeAnchor(anchorVerdict, classification, manifest.AnchorUnavailableReason))

	code := exitCode(result, anchorVerdict)
	_, _ = fmt.Fprintf(out, "verdict: %s (exit %d)\n", verdictLabel(code), code)
	return code, nil
}

// exitCode implements the priority ladder in the package doc's exit-code
// table: BROKEN (1) beats TRUNCATION_SIGNATURE (2) beats both-clean (0)
// beats everything else (3, degraded/warning).
func exitCode(result verify.Result, anchorVerdict string) int {
	switch {
	case result.Outcome == verify.OutcomeBroken:
		return exitBroken
	case anchorVerdict == "TRUNCATION_SIGNATURE":
		return exitTruncation
	case result.Outcome == verify.OutcomeIntactAttested && anchorVerdict == "IN_SYNC":
		return exitOK
	default:
		return exitDegraded
	}
}

func verdictLabel(code int) string {
	switch code {
	case exitOK:
		return "OK"
	case exitBroken:
		return "TAMPERED"
	case exitTruncation:
		return "TRUNCATION SUSPECTED"
	default:
		return "DEGRADED"
	}
}

// classifyLabel maps anchor.Classification.Divergence (PLUS the
// independent coverage-gap fact — see anchor.Classify's own doc comment on
// why a caller must check UnwitnessedHeadSeqs separately from Divergence)
// onto the token set the package doc's exit-code table names. Reuses
// anchor.Classify unchanged; this function only names its output, it never
// recomputes the classification itself (that would be exactly the
// duplicated-detector defect class this release exists to close).
func classifyLabel(c anchor.Classification) string {
	switch c.Divergence {
	case anchor.DivergenceTruncation:
		return "TRUNCATION_SIGNATURE"
	case anchor.DivergenceBenignCrash:
		return "BENIGN_LAG"
	case anchor.DivergenceWriterBroken:
		return "WRITER_BROKEN"
	default: // anchor.DivergenceNone
		if len(c.UnwitnessedHeadSeqs) > 0 {
			// Defensively unreachable per anchor.Classify's own documented
			// invariant (DivergenceNone requires no coverage gap) — kept as
			// its own labeled branch rather than silently falling through
			// to IN_SYNC, in case that invariant is ever relaxed.
			return "COVERAGE_GAP"
		}
		return "IN_SYNC"
	}
}

// describeChain renders Result as a single human-readable line. keyCount is
// printed as a COUNT — never key material (SEC-44) — so the report states
// how many keys were consulted without ever showing what they are.
func describeChain(r verify.Result, keyCount int) string {
	switch r.Outcome {
	case verify.OutcomeIntactAttested:
		return fmt.Sprintf("INTACT+ATTESTED — %d event(s) verified, attested through seq %d (verified against %d supplied key(s))",
			r.EventsVerified, r.AttestedThroughSeq, keyCount)
	case verify.OutcomeIntactUnattested:
		return fmt.Sprintf("INTACT+UNATTESTED — %d event(s) verified, attested through seq %d, UNATTESTED from seq %d onward (no signed checkpoint covers the tail yet)",
			r.EventsVerified, r.AttestedThroughSeq, r.UnattestedFromSeq)
	default: // verify.OutcomeBroken
		if r.BrokenAtIndex >= 0 {
			return fmt.Sprintf("BROKEN — first divergence at event index %d (seq=%d id=%s): recomputed chain hash does not match the stored value",
				r.BrokenAtIndex, r.BrokenEventSeq, r.BrokenEventID)
		}
		if r.UnknownKeyID {
			return fmt.Sprintf("BROKEN — head at seq %d was signed under key id %s, which does not match any of the %d supplied key(s) (a rotation/operator question per ADR-0069 D5, not by itself a tamper finding — but this key cannot verify it)",
				r.BadHeadSeq, r.BadHeadKeyID, keyCount)
		}
		return fmt.Sprintf("BROKEN — head at seq %d (key id %s) failed Ed25519 signature verification",
			r.BadHeadSeq, r.BadHeadKeyID)
	}
}

// describeAnchor renders the anchor axis. HeadsBehind is always printed as
// a head COUNT, never a seq subtraction — the T-126a bug this ticket must
// not reintroduce.
func describeAnchor(verdict string, c anchor.Classification, unavailableReason string) string {
	var msg string
	switch verdict {
	case "ABSENT":
		msg = fmt.Sprintf("ABSENT — the export carried no anchor section (%s); truncation cannot be independently detected", unavailableReason)
	case "IN_SYNC":
		msg = "IN_SYNC — the anchor's last recorded head agrees with the exported heads"
	case "TRUNCATION_SIGNATURE":
		msg = "TRUNCATION_SIGNATURE — the anchor's last recorded position is AHEAD of the exported heads; events may have been deleted"
	case "BENIGN_LAG":
		msg = fmt.Sprintf("BENIGN_LAG — anchor is %d head(s) behind (ordinary crash-window lag, self-heals; not a tamper finding)", c.HeadsBehind)
	case "WRITER_BROKEN":
		msg = fmt.Sprintf("WRITER_BROKEN — anchor is %d head(s) behind (operational alarm — disk/permissions/path — not a tamper finding)", c.HeadsBehind)
	case "COVERAGE_GAP":
		msg = "COVERAGE_GAP — the anchor's tips agree with the exported heads, but part of the history was never independently witnessed"
	default:
		msg = verdict
	}
	if len(c.UnwitnessedHeadSeqs) > 0 && verdict != "COVERAGE_GAP" {
		msg += fmt.Sprintf("; additionally, %d head(s) predate this anchor's coverage and were never independently witnessed (seqs %v)", len(c.UnwitnessedHeadSeqs), c.UnwitnessedHeadSeqs)
	}
	return msg
}

// openSeekable resolves path to a seekable *os.File. "-" means stdin: since
// stdin is not generally seekable but the two-pass design (package doc)
// requires seeking back to the start, stdin is buffered ONCE to a temp file
// on local disk — bounded by disk, not by process memory, which is the
// property PERF-16/D6 actually require (memory must not scale with the
// customer's chain; disk is unconstrained by that rule). The temp file is
// removed by the returned cleanup func.
func openSeekable(path string) (*os.File, func(), error) {
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		return f, func() { _ = f.Close() }, nil
	}

	tmp, err := os.CreateTemp("", "aireos-verify-stdin-*.ndjson")
	if err != nil {
		return nil, nil, fmt.Errorf("buffer stdin: create temp file: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	if _, err := io.Copy(tmp, os.Stdin); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("buffer stdin: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("buffer stdin: seek: %w", err)
	}
	return tmp, cleanup, nil
}
