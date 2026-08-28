#!/usr/bin/env python3
"""go-deps-check — the core's Go dependency surface stays small enough to read.

Implements the enforcement **ADR-0073 §2** relies on and which, at the time that
ADR was Accepted, DID NOT EXIST. ADR-0073's review triggers say so in writing:
its §2 trigger ("the core acquires an inference, embedding, or model-serving
dependency") was **manual**, because `tools/forbidden-deps.mjs` inspects
`apps/*/package.json` for JS meta-frameworks and cannot see a Go module, and
`tools/forbidden-features-check` is a path-existence check for ADR-0046. Naming
a gate that does not cover the case is the recurring "a correct control, and its
caller was never checked" defect; this closes it.

**Allowlist, not denylist — deliberately broader than ADR-0073 §2 requires.**
A denylist of model/embedding packages would only catch the categories someone
thought to name. §2's actual property is that the dependency surface *stays small
enough to read*, and CLAUDE.md §Project doctrine already requires an ADR for
every major dependency choice. So the gate fails on ANY new direct dependency:
adding one is already an ADR-gated decision, and it should cost a one-line edit
here too. That is the point, not friction.

⚠ **Parses BOTH `require (...)` blocks AND single-line `require X v` forms.**
This is not incidental. The original survey behind ADR-0073 used a `sed` range
that matched only the block form, silently missed
`packages/go/migrate`'s single-line `require github.com/golang-migrate/migrate/v4`,
and the ADR consequently states "nine third-party modules" where the true count
is **TEN**. An instrument that cannot see a legal form of its input reports a
confident wrong answer - ops rule 12.

Usage:  python infra/scripts/go_deps_check.py
Exit 0 = every direct dependency is allowlisted. Exit 1 = at least one is not,
each printed with the module that requires it.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]

# Every third-party direct dependency of the Go core, verified against the tree
# 2026-08-28. ADDING AN ENTRY HERE REQUIRES AN ADR (CLAUDE.md §Project doctrine:
# "All major dependency choices need an ADR"). Removing one needs nothing.
ALLOWED = {
    "aidanwoods.dev/go-paseto",              # ADR-0046 capability tokens
    "github.com/coder/websocket",            # ADR-0048 cockpit data plane
    "github.com/go-chi/chi/v5",              # ADR-0043 API framework
    "github.com/golang-migrate/migrate/v4",  # ADR-0044 migrations
    "github.com/google/uuid",
    "github.com/jackc/pgx/v5",               # Postgres driver
    "github.com/oapi-codegen/runtime",       # ADR-0043 generated stubs
    "github.com/pkg/sftp",                   # ADR-0071 governed file transfer
    "golang.org/x/crypto",                   # ADR-0048 SSH; ⚠ residual R6 - held at v0.33.0
    "golang.org/x/net",
}
INTERNAL_PREFIX = "aireos.local/"

REQ_BLOCK = re.compile(r"^require\s*\((.*?)^\)", re.M | re.S)
REQ_LINE = re.compile(r"^require\s+(\S+)\s+\S+\s*$", re.M)


def direct_requires(gomod: pathlib.Path) -> set[str]:
    """Direct (non-indirect) requires, from BOTH require forms."""
    text = gomod.read_text(encoding="utf-8")
    found: set[str] = set()
    for blk in REQ_BLOCK.finditer(text):
        for line in blk.group(1).splitlines():
            line = line.strip()
            if not line or line.startswith("//") or "// indirect" in line:
                continue
            found.add(line.split()[0])
    for m in REQ_LINE.finditer(text):
        found.add(m.group(1))
    return found


def main() -> int:
    work = (ROOT / "go.work").read_text(encoding="utf-8")
    modules = re.findall(r"^\t\./(\S+)$", work, re.M)
    if not modules:
        sys.exit("go-deps-check ABORT: no `use` directives parsed from go.work")

    violations: list[str] = []
    third_party: set[str] = set()
    scanned = 0

    for mod in modules:
        gomod = ROOT / mod / "go.mod"
        if not gomod.exists():
            violations.append(f"{mod}: go.work lists it but go.mod is missing")
            continue
        scanned += 1
        for dep in sorted(direct_requires(gomod)):
            if dep.startswith(INTERNAL_PREFIX):
                continue
            third_party.add(dep)
            if dep not in ALLOWED:
                violations.append(f"{mod}: unallowlisted direct dependency  {dep}")

    if violations:
        print("go-deps-check FAIL — the core's dependency surface changed:")
        for v in sorted(set(violations)):
            print(f"  {v}")
        print("\nADR-0073 §2: the core's dependency surface stays small enough to read, and"
              "\nsubstrates (inference / execution / agent) are governed, NOT vendored into it."
              "\nIf this dependency is intended, it needs an ADR (CLAUDE.md §Project doctrine)"
              "\nand an entry in ALLOWED with the ADR named beside it.")
        return 1

    stale = sorted(ALLOWED - third_party)
    note = f"; {len(stale)} allowlisted but unused ({', '.join(stale)})" if stale else ""
    print(f"go-deps-check OK — {len(third_party)} third-party direct dependencies across "
          f"{scanned} go.work modules, all allowlisted{note}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
