#!/usr/bin/env python3
"""integration-suite-check (release-criteria VER-38/VER-42, T-159).

Why this exists: `task test-integration` used to be seven hand-written
`go test -tags integration <path>/...` lines. The count of `//go:build
integration` files in the tree moved from 19 to 21 between v0.15.0 scoping
and this ticket, and nobody updated the Taskfile -- the hand list silently
drifted two suites behind the tree (`services/mcp-gateway/internal/server`,
`services/session-broker/internal/store`). The fix here is DERIVATION: walk
the tree for the build tag and run whatever is found, so a new integration
file can never again ship uncounted.

⭐ Derivation alone is not the fix -- it is HALF the fix, and the dangerous
half if shipped alone. Every one of these suites self-skips (`t.Skip(...)`)
when its own `*_TEST_DATABASE_URL` env var is unset. A naive
`go test -tags integration ./...` "runs" all of them and silently skips
almost all of them when the DSNs aren't configured -- a gate that LOOKS
complete (it iterated every discovered suite!) while verifying almost
nothing. That is strictly worse than the honest hand-list it replaces,
because the hand-list's staleness was at least visible on read. So this
script's second half is the one that matters: for every discovered
integration test FUNCTION (not just every discovered package -- see below),
assert it actually executed (`pass` or `fail`), and FAIL LOUDLY, naming
exactly which suites self-skipped, if any did not.

Why per-TEST-FUNCTION rather than per-package: a package can carry both
`go:build integration` files and ordinary always-run test files side by
side (`-tags integration` only ADDS files gated on that tag; it does not
remove anything). A per-package check ("did anything in this package pass?")
would go green off an ordinary passing unit test in the same directory while
the actual integration test silently skipped beside it -- the exact shape of
lie this ticket exists to prevent. So the check named here is min-grained:
extract every `func TestXxx(t *testing.T)` defined in a tagged file, run the
package under `-tags integration -json`, and require that EXACT function
name to report a `pass` or `fail` action. A `skip` action, or no action at
all (e.g. a build failure), is a hard FAIL for the gate.

How "actually ran" is asserted (the design choice, stated per VER-42's ask):
`go test -tags integration -json` is run once per Go module that contains a
discovered suite (these are 7 independent modules, not one workspace), and
its NDJSON event stream is parsed for each named test's terminal action
(pass/fail/skip). This was chosen over a static "required env var" map
because a static map is itself exactly the kind of hand-maintained list this
ticket is closing -- it can drift from what the test file actually checks.
Reading the test's own recorded outcome cannot drift: it is authoritative
about whether the assertions inside that function actually ran, for
whatever reason (missing DSN, wrong DSN, missing migration, ...), without
this script needing to know or guess why.

Usage:
  python infra/scripts/integration_suite_check.py                # discover + run + assert
  python infra/scripts/integration_suite_check.py --discover-only  # list suites, run nothing
Exit 0 = every discovered integration test function reported `pass`.
Exit 1 = at least one reported `skip`, `fail`, or never reported at all
         (build failure / crash) -- printed with the exact reason.
"""
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]  # infra/scripts/ -> repo root

PRUNE_DIRS = {"node_modules", ".git", "dist", "build", ".task", "bin", ".venv", "__pycache__", "vendor"}
BUILD_TAG_LINE = "//go:build integration"
TEST_FUNC_RE = re.compile(r"^func\s+(Test[A-Za-z0-9_]+)\s*\(\s*\w+\s+\*testing\.T\s*\)", re.M)
MODULE_LINE_RE = re.compile(r"^module\s+(\S+)", re.M)


def find_tagged_files() -> list[pathlib.Path]:
    """Every .go file carrying the integration build tag, anywhere in the tree."""
    hits = []
    for dirpath, dirnames, filenames in __import__("os").walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in PRUNE_DIRS]
        for fn in filenames:
            if not fn.endswith(".go"):
                continue
            p = pathlib.Path(dirpath) / fn
            try:
                head = p.read_text(encoding="utf-8", errors="ignore")
            except OSError:
                continue
            # The build-tag comment must be a standalone line near the top,
            # per `gofmt`/`go vet` convention (blank line before `package`).
            if any(line.strip() == BUILD_TAG_LINE for line in head.splitlines()[:5]):
                hits.append(p)
    return sorted(hits)


def module_root_for(path: pathlib.Path) -> pathlib.Path:
    d = path.parent
    while d != d.parent:
        if (d / "go.mod").exists():
            return d
        d = d.parent
    sys.exit(f"integration-suite-check ABORT: no go.mod found above {path}")


def module_name(mod_root: pathlib.Path) -> str:
    text = (mod_root / "go.mod").read_text(encoding="utf-8")
    m = MODULE_LINE_RE.search(text)
    if not m:
        sys.exit(f"integration-suite-check ABORT: {mod_root}/go.mod has no `module` line")
    return m.group(1)


class Suite:
    def __init__(self, file: pathlib.Path, mod_root: pathlib.Path, tests: list[str]):
        self.file = file
        self.mod_root = mod_root
        self.pkg_dir = file.parent  # absolute
        self.tests = tests  # test function names defined in THIS file

    @property
    def rel_pkg(self) -> str:
        return self.pkg_dir.relative_to(self.mod_root).as_posix()

    @property
    def import_path(self) -> str:
        rel = self.rel_pkg
        base = module_name(self.mod_root)
        return base if rel == "." else f"{base}/{rel}"


def discover() -> list[Suite]:
    suites = []
    for f in find_tagged_files():
        text = f.read_text(encoding="utf-8", errors="ignore")
        tests = TEST_FUNC_RE.findall(text)
        if not tests:
            # Tagged file with no Test function (e.g. a shared helper) --
            # nothing to assert "ran"; note it but don't invent a test name.
            continue
        suites.append(Suite(f, module_root_for(f), tests))
    return suites


def group_by_module(suites: list[Suite]) -> dict[pathlib.Path, list[Suite]]:
    grouped: dict[pathlib.Path, list[Suite]] = {}
    for s in suites:
        grouped.setdefault(s.mod_root, []).append(s)
    return grouped


def run_module(mod_root: pathlib.Path, suites: list[Suite]) -> tuple[dict[tuple[str, str], str], str]:
    """Run every discovered package in this module once under -tags integration
    -json; return {(import_path, test_name): terminal_action} plus raw log text
    (for diagnostics on failure -- never discarded silently)."""
    targets = sorted({f"./{s.rel_pkg}/..." for s in suites})
    # `-p 1`: several of these suites share unscoped global state in the same
    # Postgres tables (e.g. mcp-gateway's ListPending has no per-test scoping) --
    # the ORIGINAL hand-written Taskfile lines ran one `go test` per package,
    # which serialized them as a side effect. Batching every discovered package
    # of a module into ONE invocation (done here so N packages cost 1 process,
    # not N) reintroduces that missing serialization as `go test`'s default
    # cross-package parallelism -- found live in this ticket (T-159): with
    # packages run in parallel, mcp-gateway's server test intermittently picked
    # up a pending invocation ROW CREATED BY THE CONCURRENTLY RUNNING store
    # test (both write the same shared `invocations` table), reading a
    # decrypted command that belonged to a different test entirely. `-p 1`
    # restores the serialized-by-construction behaviour the hand list had.
    proc = subprocess.run(
        ["go", "test", "-tags", "integration", "-json", "-p", "1", *targets],
        cwd=mod_root, capture_output=True, text=True,
    )
    outcomes: dict[tuple[str, str], str] = {}
    for line in proc.stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        test = ev.get("Test")
        action = ev.get("Action")
        pkg = ev.get("Package")
        if not test or action not in ("pass", "fail", "skip"):
            continue
        outcomes[(pkg, test)] = action  # last write wins -> terminal action
    log = f"--- go test -tags integration -json {' '.join(targets)} (cwd={mod_root}) ---\n" \
          f"{proc.stdout}\n{proc.stderr}\nexit={proc.returncode}\n"
    return outcomes, log


def main() -> int:
    discover_only = "--discover-only" in sys.argv[1:]
    suites = discover()
    total_tests = sum(len(s.tests) for s in suites)
    print(f"integration-suite-check: discovered {len(suites)} tagged file(s) / "
          f"{total_tests} test function(s) across {len(group_by_module(suites))} module(s):")
    for s in suites:
        rel = s.file.relative_to(ROOT).as_posix()
        print(f"  {rel}  [{', '.join(s.tests)}]")

    if discover_only:
        print("integration-suite-check: --discover-only, nothing executed.")
        return 0

    ran, skipped, failed, missing = [], [], [], []
    logs = []
    for mod_root, mod_suites in sorted(group_by_module(suites).items()):
        outcomes, log = run_module(mod_root, mod_suites)
        logs.append(log)
        for s in mod_suites:
            for t in s.tests:
                key = (s.import_path, t)
                action = outcomes.get(key)
                label = f"{s.import_path}.{t}"
                if action == "pass":
                    ran.append(label)
                elif action == "fail":
                    failed.append(label)
                elif action == "skip":
                    skipped.append(label)
                else:
                    missing.append(label)

    print("--------------------------------------------------------------")
    print(f"integration-suite-check SUMMARY: {total_tests} discovered, "
          f"{len(ran)} ran+passed, {len(failed)} ran+FAILED, "
          f"{len(skipped)} SELF-SKIPPED, {len(missing)} MISSING (build failure "
          f"or crash -- never reported an outcome).")
    for label in failed:
        print(f"  FAIL     {label} -- executed and reported a test failure (real rot, "
              f"not a config gap -- see the go test log above).")
    for label in skipped:
        print(f"  SKIPPED  {label} -- self-skipped (its *_TEST_DATABASE_URL env var is "
              f"unset, or the test itself chose to skip). This suite verified NOTHING "
              f"this run.")
    for label in missing:
        print(f"  MISSING  {label} -- never reported pass/fail/skip at all (build "
              f"failure, panic, or timeout). See the go test log above.")

    if failed or skipped or missing:
        print("\n".join(logs))
        print("integration-suite-check FAIL -- see FAIL/SKIPPED/MISSING line(s) above. "
              "A green run of this gate requires every discovered integration test to "
              "have actually executed and passed, not merely to have been iterated over.")
        return 1

    print(f"integration-suite-check OK -- all {total_tests} discovered integration test "
          f"function(s), across {len(suites)} tagged file(s), executed and passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
