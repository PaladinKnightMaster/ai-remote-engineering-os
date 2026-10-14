#!/usr/bin/env python3
"""doc-check (release-criteria DOC-1, §11a-1; CSV leg VER-15/T-132; roots leg
VER-36/VER-43/T-160; public-docs leg T-161).

Assert every in-repo relative path referenced in CANONICAL_SOURCE_OF_TRUTH.md,
every docs/adr/*.md, every docs/executive/*.md, every docs/releases/**/*.md
(nested per-version subdirectories -- rglob, NOT glob), every
docs/requirements/*.md, and the root-level public documents (README.md,
SECURITY.md, CONTRIBUTING.md) resolves on disk, so the canonical index, the ADR
ladder, the executive tier, the release/requirements corpus, and the documents
a public stranger reads first never drift from the tree.

README.md/SECURITY.md/CONTRIBUTING.md were added at T-161 (v0.16.0, PUB-4/
PUB-3/PUB-8): before this, the three documents a stranger depends on most to
install and trust the product were the LEAST gated files in the repo -- this
release's own thesis (can somebody who is not us rely on what we claim)
pointed at itself. Checked unmodified against the as-written files: all
references resolve; no break surfaced. LICENSE and NOTICE are deliberately
NOT added -- LICENSE is verbatim upstream Apache-2.0 boilerplate with no
in-repo references, and NOTICE's one doc pointer is plain prose, not a
backtick span or markdown link, so candidates() would not see it as a
checkable reference anyway.

docs/executive/ was added 2026-08-27 (canon work order pass 3): those four files
were rewritten to POINT at authoritative sources rather than duplicate them --
duplication is why they drifted for fifteen months -- which makes every one of
their links load-bearing. An ungated pointer is the previous defect wearing a
different hat.

docs/releases/**/*.md and docs/requirements/*.md were added at T-160 (VER-36
closed, VER-43). VER-36's scoping estimate (44 files / 217 references, 10
broken) was measured weeks earlier by hand and went stale: T-153 shipped in
the interim, and v0.16.0 added new documents. Re-measured at T-160 against
the actual candidates()/resolves() logic below: 48 files, 153 references, ONE
broken -- `docs/releases/v0.15.0/v0.15.0-security-review.md`'s citation
`services/policy-service/internal/server/server.go:182-191` had an accidental
markdown line-wrap splitting the backtick span itself across two lines, so the
CODE regex (which does not match across newlines) never saw it as one token.
The path was, and remains, entirely valid; the wrap was fixed as a pure
reflow (zero words changed) rather than left broken, because nothing about
the underlying claim was stale -- only its typesetting. None of the other
three break classes VER-36 anticipated (transient `tests/smoke/test-results/`
build artifacts, the renamed `services/workstream-engine`, `known-issues.mdx`)
produced a NEW break here -- Commander review, 2026-09-02: correcting an
initial (wrong) claim that they were absent from the .md corpus -- they are
PRESENT (10/9/3 files respectively). They produce no break because has_ext()
returns False for all three: they carry no extension EXTS recognises (a bare
directory reference, and `.mdx` != `.md`), so candidates() never treats them
as checkable path references at all, before or after this ticket. That is a
real ungated population, not an absent one -- see VER-48, deliberately left
open rather than fixed here (broadening has_ext() to extension-less
directory/module references is a semantics change with false-positive risk,
out of this ticket's scope).

Scope (pragmatic, low false-positive). Two reference kinds are checked:
  * markdown link targets `](target)` — meant to be resolvable; and
  * backtick spans that are unambiguous repo-relative FILE paths: they begin with
    a known top-level dir AND end in a known file extension.
This deliberately excludes code symbols (e.g. `packages/go/auth/Signer`), bare
directory references (`packages/go/events/`), and abbreviated prose names
(`module-map.md`) — none are unambiguous file paths. Also skipped: URLs, pure
anchors, absolute paths, sibling-repo (`../`) refs (the kit may be absent in CI),
and placeholders (containing `<`, `>`, `*`, `{`, `}`, `$`, or whitespace). A
reference resolves if it exists relative to the repo root OR the referencing
file's directory. Exits non-zero, listing every unresolved reference.

CSV leg (VER-15 §fourth instance, T-132): every `.csv` under
docs/requirements/ must parse and carry a uniform field count per row. Before
this leg existed, `.csv` appeared in this script ONLY as a scannable path
extension (see EXTS below) — nothing here ever opened a CSV and looked inside
it. That gap let a malformed row into docs/requirements/backlog_v4.csv at
`239c0e7` unnoticed by any automated gate; the Commander hand-caught it with a
throwaway `csv`-module script during the T-131 review. This automates exactly
that check, with a failure message naming the file, the row, and both field
counts, so the next person doesn't spend the same hour.
"""
import csv
import re
import sys
import pathlib

ROOT = pathlib.Path(__file__).resolve().parents[2]  # infra/scripts/ -> repo root

TOP = (
    "docs/", "services/", "packages/", "apps/", "infra/", "tests/", "ai/",
    "references/", ".github/",
)
EXTS = (
    ".md", ".go", ".yaml", ".yml", ".sql", ".json", ".sh", ".ts", ".tsx",
    ".py", ".csv", ".toml", ".mod", ".work", ".txt", ".mjs",
)

LINK = re.compile(r"\]\(([^)]+)\)")
CODE = re.compile(r"`([^`]+)`")
# Fenced code blocks are removed before CODE matching (2026-10-14, v0.16.0 PUB-6 fix
# pass). CODE pairs single backticks across the whole text, so a ``` fence (three
# backticks) shifts the pairing by one: after an odd fence, every inline `path` span
# was paired with the wrong backtick, swallowed whitespace and was skipped -- silently.
# Found by a planted broken reference in the quickstart that did NOT fail the gate.
# Measured: 27 references across the corpus were invisible, one of them broken.
FENCE = re.compile(r"^[ \t]*(```|~~~).*?^[ \t]*\1[^\n]*$", re.M | re.S)

# NOTE: "../" is deliberately NOT here. It was, until 2026-08-27, and that
# blanket-skipped every in-repo parent-relative link -- 18 of them in the
# rewritten docs/executive/ alone, silently unchecked. The skip exists only
# for SIBLING-REPO refs (../agency-kit/, ../artifacts/), which may be absent
# in CI; resolves() now distinguishes those by whether the path escapes ROOT.
SKIP_PREFIX = ("http://", "https://", "#", "mailto:", "/", "~")


# A provenance citation carries a line suffix: `path.md:42`, `path.go:12-30`,
# `catalog.go:130,213,321,478`. docs/adr/TEMPLATE.md MANDATES this form for every
# load-bearing claim, so it is the highest-value reference population in the corpus
# -- and until 2026-08-28 every one of them was INVISIBLE here: has_ext() tested the
# last path segment, which ends in the line number, not the extension, so the
# candidate was dropped before it was ever resolved. Found by a negative control
# (a planted broken citation in ADR-0074 did not fire the gate); 15 such citations
# existed at the time, none broken -- an ungated population, not live rot.
LINE_SUFFIX = re.compile(r":\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*$")


def strip_line_suffix(s: str) -> str:
    """Drop a trailing :line / :start-end / :a,b,c provenance suffix."""
    return LINE_SUFFIX.sub("", s)


def has_ext(s: str) -> bool:
    last = strip_line_suffix(s).rsplit("/", 1)[-1]
    return any(last.endswith(e) for e in EXTS)


def candidates(text: str) -> set[str]:
    """Return checkable references: all markdown link targets + backtick spans
    that are unambiguous repo-relative file paths (top-level dir + file ext)."""
    out: set[str] = set()
    for m in LINK.finditer(text):
        ref = m.group(1).strip()
        if ref and not ref.startswith(SKIP_PREFIX) and not any(c in ref for c in "<>*{}$ \t"):
            out.add(ref)
    for m in CODE.finditer(FENCE.sub("", text)):
        ref = m.group(1).strip()
        if any(c in ref for c in "<>*{}$ \t") or ref.startswith(SKIP_PREFIX):
            continue
        if ref.startswith(TOP) and has_ext(ref):
            out.add(ref)
    return out


def escapes_repo(ref: str, srcdir: pathlib.Path) -> bool:
    """True if a parent-relative ref points OUTSIDE the repo (a sibling-repo ref
    such as ../agency-kit/ or ../artifacts/, which may legitimately be absent)."""
    try:
        return ROOT not in (srcdir / ref).resolve().parents and (srcdir / ref).resolve() != ROOT
    except (OSError, ValueError):
        return True


def resolves(ref: str, srcdir: pathlib.Path) -> bool:
    ref = strip_line_suffix(ref.split("#", 1)[0]).rstrip("/").rstrip(".,;:")
    if not ref:
        return True
    # Sibling-repo refs are out of scope; in-repo parent-relative refs are NOT.
    if ref.startswith("../") and escapes_repo(ref, srcdir):
        return True
    return (ROOT / ref).exists() or (srcdir / ref).exists()


def check_csvs() -> list[str]:
    """Every .csv under docs/requirements/ AND references/ must parse and have a
    uniform field count per row (VER-15 §fourth instance). Returns one message per
    malformed row, naming the file, the row number (1-based, header = row 1),
    the expected field count (the header's), and the actual one.

    references/ added 2026-08-28: references_v4.csv carries every external-tool
    ADOPTION VERDICT and had no well-formedness gate at all, while the
    requirements CSVs beside it did. Same gap VER-15's fourth instance closed for
    docs/requirements/, where a malformed row reached backlog_v4.csv unnoticed by
    every automated gate and was hand-caught in review."""
    issues: list[str] = []
    targets = sorted((ROOT / "docs" / "requirements").glob("*.csv")) +         sorted((ROOT / "references").glob("*.csv"))
    for f in targets:
        rel = f.relative_to(ROOT)
        try:
            with f.open(newline="", encoding="utf-8") as fh:
                rows = list(csv.reader(fh))
        except csv.Error as exc:
            issues.append(f"{rel}: failed to parse as CSV — {exc}")
            continue
        if not rows:
            continue
        expected = len(rows[0])
        for i, row in enumerate(rows[1:], start=2):
            if len(row) != expected:
                issues.append(
                    f"{rel}  row {i}: expected {expected} fields (per header), "
                    f"found {len(row)}"
                )
    return issues


def main() -> int:
    # VER-45 (T-162): this `sources` list is HAND-MAINTAINED and WILL DRIFT — it is
    # named here explicitly rather than derived, because deriving it (e.g. globbing
    # every *.md in the tree) was already assessed and rejected as VER-48: `has_ext()`
    # would silently treat extension-less references as unresolved, and directories
    # like `docs/security/`, `docs/devops/`, `docs/spike/`, or `docs/api-contracts/`
    # would need their own opt-in evaluation for false positives before being added,
    # not a blanket sweep. When a new documentation corpus is created and its claims
    # should be checked, add ONE line below and state why, following the pattern of
    # every entry already here.
    sources = [ROOT / "CANONICAL_SOURCE_OF_TRUTH.md"]
    sources += sorted((ROOT / "docs" / "adr").glob("*.md"))
    # docs/executive/ added 2026-08-27: the four files there were rewritten to
    # POINT at authoritative sources instead of duplicating them, so every link
    # is load-bearing and an unresolved one is a silent regression.
    sources += sorted((ROOT / "docs" / "executive").glob("*.md"))
    # docs/releases/ added at T-160 (VER-36/VER-43): NESTED per-version
    # subdirectories -- rglob(), not glob(), or this would silently match zero
    # files and ship a "passing" extension that scans nothing new.
    sources += sorted((ROOT / "docs" / "releases").rglob("*.md"))
    # docs/requirements/*.md added at T-160 alongside the releases leg: the
    # existing CSV leg (check_csvs) already covers docs/requirements/*.csv;
    # this covers the markdown in the same directory (currently just
    # epic_story_catalog_v4.md, the live planning record DOC-17 corrects).
    sources += sorted((ROOT / "docs" / "requirements").glob("*.md"))
    # Root-level public documents added at T-161 (PUB-3/PUB-4/PUB-8): the docs
    # a stranger reads first, previously scanned by nothing at all.
    sources += [ROOT / "README.md", ROOT / "SECURITY.md", ROOT / "CONTRIBUTING.md"]
    # The three user-path guides README links (v0.16.0 PUB-6 fix pass, 2026-10-14):
    # the install guide, postgres guide and quickstart a stranger actually follows.
    # Named individually, not docs/devops/*.md, per the VER-45/VER-48 note above.
    sources += [ROOT / "docs" / "devops" / n for n in (
        "offline-install-v3.md", "offline-postgres-install.md",
        "quickstart-first-governed-action.md")]

    missing: list[str] = []
    checked = 0
    for f in sources:
        if not f.exists():
            missing.append(f"(missing source) {f}")
            continue
        for ref in candidates(f.read_text(encoding="utf-8")):
            checked += 1
            if not resolves(ref, f.parent):
                missing.append(f"{f.relative_to(ROOT)}  ->  {ref}")

    csv_issues = check_csvs()
    csv_count = (len(list((ROOT / "docs" / "requirements").glob("*.csv")))
                 + len(list((ROOT / "references").glob("*.csv"))))

    ok = True
    if missing:
        ok = False
        print("doc-check FAIL — unresolved in-repo path references:")
        for m in sorted(set(missing)):
            print(f"  {m}")
    if csv_issues:
        ok = False
        print("doc-check FAIL — malformed CSV row(s) under docs/requirements/:")
        for m in csv_issues:
            print(f"  {m}")
    if not ok:
        return 1

    print(f"doc-check OK — {checked} in-repo path references resolve "
          f"(across {len(sources)} files); {csv_count} CSVs under "
          f"docs/requirements/ + references/ well-formed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
