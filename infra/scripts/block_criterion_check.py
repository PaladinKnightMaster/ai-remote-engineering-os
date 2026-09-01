#!/usr/bin/env python3
"""block-criterion-check (release-criteria VER-41, SEC-61, VER-44; T-158).

Why this exists: v0.15.0 shipped **SEC-57** ("a rule cannot widen authority"),
marked `block`, with zero tests anywhere in the repo -- found late, by a human
grepping during a security review, not by any gate. This script is the
mechanical version of that human: for every criterion this gate covers, it
asserts a NAMED test actually exists in the tree, and fails loudly if the
criterion carries no such assertion.

Marker convention (deliberately NOT prose-parsing -- see below):
  A covered criterion's own table row must carry, inline, one of:
    `[test: SomeIdentifier]`  -- a test/function named exactly `SomeIdentifier`
                                  must be DEFINED somewhere in the tree
                                  (`func SomeIdentifier(`, `def SomeIdentifier(`,
                                  or `function SomeIdentifier(`).
    `[test: none -- <reason>]` -- an EXPLICIT, human-authored acknowledgment
                                  that no standing test backs this criterion,
                                  with a reason. Printed distinctly, never
                                  silently treated as passing.
  A covered row with NEITHER form present is a hard FAIL -- that is exactly
  SEC-57's shape: nothing at all, not even an acknowledgment.

  Why not scrape test names out of the criterion's English prose (e.g. "proven
  by TestFoo in bar_test.go")? `doc-check` already learned this lesson the hard
  way: its `has_ext()` silently skipped every `[read: path:line]` citation for
  months because it tested the wrong substring. Prose is for humans and drifts
  without anyone noticing; an explicit bracketed marker, in the same family as
  this corpus's own `[read: file:line]` citation convention, is the only thing
  worth asserting against mechanically.

Scope decision (deliberate, and stated in the output every run):
  Covered population = every `block`-type row whose criterion ID carries the
  `SEC-` prefix, ANYWHERE in the document. `SEC-` is this corpus's own
  established tag for negative/security proofs across v0.13.0-v0.16.0 -- the
  exact population SEC-57 sat in -- and it is the only prefix whose rows
  assert a discrete, checkable runtime/security PROPERTY as opposed to a
  documentation, process, or UX requirement. `DOC-*`, `PUB-*`, `FE-*` rows
  (e.g. DOC-14/DOC-16: "release notes must say X") are NOT checkable by a
  named test and are deliberately excluded -- demanding one would produce
  false failures and this gate would be disabled within a release. `VER-*`,
  `CON-*`, `AUD-*` rows are also excluded: many assert suite-level or
  multi-file properties ("WD-1..15 regression green", "four doc-tier gates
  pass") with no single naming test, and forcing a fake single name onto them
  would be worse than the gap it claims to close. Every non-SEC block row is
  still counted and printed as SKIPPED, with the reason, so a narrow scope is
  never mistaken for a clean sweep (the same defect this release is about).

  `[test: none -- ...]` is expected to be RARE. It exists only for the small
  class of criteria that are themselves about one of this project's own
  verification INSTRUMENTS (a gate proving a gate) -- proven once, by
  deliberately mutating the instrument and observing it fail (VER-44), never
  re-run as a standing regression. `history_secret_scan.py`'s SEC-60 proof
  (T-157: plant a secret, observe the FINDING, `git reset --hard` the scratch
  commit) is the precedent: scanning full git history is not something a unit
  test can cheaply fabricate. A criterion asserting a normal runtime/security
  behaviour (SEC-57's shape) is expected to use `[test: Name]`, not `none`.

Usage:
  python infra/scripts/block_criterion_check.py             # latest release
  python infra/scripts/block_criterion_check.py PATH [PATH...]  # explicit doc(s)
Exit 0 = every covered row is test-backed or explicitly acknowledged.
Exit 1 = at least one covered row is neither -- printed with the exact reason.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]  # infra/scripts/ -> repo root

PRUNE_DIRS = {"node_modules", ".git", "dist", "build", ".task", "bin", ".venv", "__pycache__"}
DEFN_EXTS = (".go", ".py", ".ts", ".tsx", ".js", ".jsx")

ROW_ID = re.compile(r"^\|\s*\*\*([A-Z]{2,5}-\d+)\*\*")
TEST_MARKER = re.compile(r"\[test:\s*(none|[A-Za-z_][A-Za-z0-9_]*)\b[^\]]*\]", re.I)
COVERED_PREFIX = "SEC-"


def cells_of(row: str) -> list[str]:
    return row.strip().strip("|").split("|")


def row_type(row: str) -> str:
    return cells_of(row)[-1].strip().strip("*").strip().lower()


def parse_rows(text: str) -> list[str]:
    return [line for line in text.splitlines() if ROW_ID.match(line)]


def latest_criteria_doc() -> pathlib.Path:
    def key(p: pathlib.Path):
        m = re.match(r"v(\d+)\.(\d+)\.(\d+)", p.parent.name)
        return tuple(int(x) for x in m.groups()) if m else (0, 0, 0)

    candidates = sorted((ROOT / "docs/releases").glob("v*/v*-release-criteria.md"), key=key)
    if not candidates:
        sys.exit("block-criterion-check ABORT: no docs/releases/v*/v*-release-criteria.md found")
    return candidates[-1]


_definition_cache: dict[str, bool] | None = None


def _build_definition_index() -> dict[str, bool]:
    """One pass over the tree collecting every `func/def/function Name(`
    definition site, so N markers cost one walk instead of N walks."""
    defn = re.compile(r"\b(?:func|def|function)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")
    found: dict[str, bool] = {}
    for dirpath, dirnames, filenames in __import__("os").walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in PRUNE_DIRS]
        for fn in filenames:
            if not fn.endswith(DEFN_EXTS):
                continue
            p = pathlib.Path(dirpath) / fn
            try:
                text = p.read_text(encoding="utf-8", errors="ignore")
            except OSError:
                continue
            for m in defn.finditer(text):
                found[m.group(1)] = True
    return found


def test_exists(name: str) -> bool:
    global _definition_cache
    if _definition_cache is None:
        _definition_cache = _build_definition_index()
    return name in _definition_cache


def check_doc(path: pathlib.Path) -> tuple[bool, str]:
    rows = parse_rows(path.read_text(encoding="utf-8"))
    block_rows = [r for r in rows if row_type(r) == "block"]
    covered, skipped = [], []
    for r in block_rows:
        (covered if ROW_ID.match(r).group(1).startswith(COVERED_PREFIX) else skipped).append(r)

    tested, exempt, fails = [], [], []
    for r in covered:
        crit_id = ROW_ID.match(r).group(1)
        m = TEST_MARKER.search(r)
        if not m:
            fails.append(f"{crit_id}: block+SEC- criterion carries no `[test: Name]` or "
                         f"`[test: none -- reason]` marker at all -- SEC-57's exact shape "
                         f"(zero tests, zero acknowledgment).")
            continue
        name = m.group(1)
        if name.lower() == "none":
            exempt.append(crit_id)
            continue
        if not test_exists(name):
            fails.append(f"{crit_id}: marker `[test: {name}]` names a test that does NOT "
                         f"exist anywhere in the tree (no `func/def/function {name}(` found).")
            continue
        tested.append((crit_id, name))

    rel = path.relative_to(ROOT) if path.is_absolute() and ROOT in path.parents else path
    lines = [
        f"block-criterion-check {rel}: {len(rows)} table rows parsed, {len(block_rows)} "
        f"block-type, {len(covered)} '{COVERED_PREFIX}*' examined "
        f"({len(tested)} test-backed, {len(exempt)} explicitly exempted), "
        f"{len(skipped)} other block row(s) SKIPPED (DOC-*/PUB-*/VER-*/FE-*/CON-*/AUD-* -- "
        f"process/doc/UX/suite-level claims, not single-test-checkable; see script docstring)."
    ]
    for crit_id, name in tested:
        lines.append(f"  OK    {crit_id} -> {name}")
    for crit_id in exempt:
        lines.append(f"  EXEMPT {crit_id} (see its row's `[test: none -- ...]` reason)")
    for f in fails:
        lines.append(f"  FAIL  {f}")
    return (not fails), "\n".join(lines)


def main() -> int:
    targets = [pathlib.Path(a) for a in sys.argv[1:]] or [latest_criteria_doc()]
    ok = True
    for t in targets:
        if not t.exists():
            sys.exit(f"block-criterion-check ABORT: {t} not found")
        good, report = check_doc(t)
        print(report)
        ok = ok and good
    if not ok:
        print("block-criterion-check FAIL -- see FAIL line(s) above.")
        return 1
    print("block-criterion-check OK -- every examined SEC-* block criterion is either "
          "test-backed (verified to exist) or an explicitly justified `[test: none]` exemption.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
