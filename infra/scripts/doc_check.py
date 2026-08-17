#!/usr/bin/env python3
"""doc-check (release-criteria DOC-1, §11a-1; CSV leg VER-15/T-132).

Assert every in-repo relative path referenced in CANONICAL_SOURCE_OF_TRUTH.md and
every docs/adr/*.md resolves on disk, so the canonical index + the ADR ladder
never drift from the tree.

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

SKIP_PREFIX = ("http://", "https://", "#", "mailto:", "/", "../", "~")


def has_ext(s: str) -> bool:
    last = s.rsplit("/", 1)[-1]
    return any(last.endswith(e) for e in EXTS)


def candidates(text: str) -> set[str]:
    """Return checkable references: all markdown link targets + backtick spans
    that are unambiguous repo-relative file paths (top-level dir + file ext)."""
    out: set[str] = set()
    for m in LINK.finditer(text):
        ref = m.group(1).strip()
        if ref and not ref.startswith(SKIP_PREFIX) and not any(c in ref for c in "<>*{}$ \t"):
            out.add(ref)
    for m in CODE.finditer(text):
        ref = m.group(1).strip()
        if any(c in ref for c in "<>*{}$ \t") or ref.startswith(SKIP_PREFIX):
            continue
        if ref.startswith(TOP) and has_ext(ref):
            out.add(ref)
    return out


def resolves(ref: str, srcdir: pathlib.Path) -> bool:
    ref = ref.split("#", 1)[0].rstrip("/").rstrip(".,;:")
    if not ref:
        return True
    return (ROOT / ref).exists() or (srcdir / ref).exists()


def check_csvs() -> list[str]:
    """Every .csv under docs/requirements/ must parse and have a uniform
    field count per row (VER-15 §fourth instance). Returns one message per
    malformed row, naming the file, the row number (1-based, header = row 1),
    the expected field count (the header's), and the actual one."""
    issues: list[str] = []
    for f in sorted((ROOT / "docs" / "requirements").glob("*.csv")):
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
    sources = [ROOT / "CANONICAL_SOURCE_OF_TRUTH.md"]
    sources += sorted((ROOT / "docs" / "adr").glob("*.md"))

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
    csv_count = len(list((ROOT / "docs" / "requirements").glob("*.csv")))

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
          f"docs/requirements/ well-formed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
