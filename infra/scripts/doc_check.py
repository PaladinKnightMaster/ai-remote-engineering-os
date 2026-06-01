#!/usr/bin/env python3
"""doc-check (release-criteria DOC-1, §11a-1).

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
"""
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

    if missing:
        print("doc-check FAIL — unresolved in-repo path references:")
        for m in sorted(set(missing)):
            print(f"  {m}")
        return 1
    print(f"doc-check OK — {checked} in-repo path references resolve "
          f"(across {len(sources)} files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
