#!/usr/bin/env python3
"""staleness-check — assert the canon's *self-descriptive* facts match the tree.

Companion to doc_check.py. That script proves every referenced path RESOLVES;
this one proves the claims the canon makes ABOUT ITSELF are still TRUE. Those are
different failure modes, and the second had no gate at all until 2026-08-27.

Why this exists (ADR-0073 session, and the record behind it):

  * CLAUDE.md said "Latest release: v0.13.0" for a full release cycle after
    v0.14.0 shipped and was tagged. Nothing noticed. CLAUDE.md is the file every
    agent reads FIRST, so a stale fact there propagates into every session.
  * ADR-0017 sat in status `Draft` through its own implementation (v0.10.0,
    ADR-0066) and was only caught fifteen months later.
  * ADR-0073's own draft asserted "ADR-0010 is still Draft" twenty minutes after
    ADR-0010 had been resolved to Accepted in the same session — two documents
    from one session contradicting each other.
  * v0.12.0 shipped a bundle version hard-coded stale for EIGHT releases,
    concealed precisely because the default happened to match it.

The generalisation, from `ops_conventions` rule 10: a control is built correctly
and **the thing that CALLS it is not updated**. A version string, an ADR head, and
a status word are all callers. This makes them fail loudly instead of drifting.

Design rules this script obeys, because it is itself a checking instrument and
that is where this project's errors concentrate (ops rules 2 + 3):

  * It prints a POSITIVE marker on success naming the assertion count — never
    silence. Silence is not evidence.
  * Every assertion is derived from the TREE (Taskfile, ADR filenames, ADR file
    contents), never from another prose document, so it cannot inherit a lie.
  * Archival trees are excluded BY PREFIX and the list is explicit: archival
    documents record what was true when written and are never rewritten to match
    a later state (ADR-0072 section 5).

Usage:  python infra/scripts/staleness_check.py
Exit 0 = every assertion holds. Exit 1 = at least one stale claim, each printed
with the file, the claim found, and the ground truth it contradicts.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]

# Archival — records of what was true when written. Never rewritten. ADR-0072 s5.
ARCHIVAL = (
    "docs/superpowers/",
    "docs/releases/",
    "docs/spec-sets/",
    "docs/deep-dive/",
    "docs/tickets/",
    "docs/evaluation/",
)

# Live docs whose self-descriptive claims must track the tree.
LIVE_DOCS = ("CLAUDE.md", "AGENTS.md", "CANONICAL_SOURCE_OF_TRUTH.md", "README.md")


def is_archival(rel: str) -> bool:
    rel = rel.replace("\\", "/")
    return any(rel.startswith(p) for p in ARCHIVAL)


def ground_truth() -> dict:
    """Every fact here is read from the tree, never from prose."""
    taskfile = (ROOT / "Taskfile.yml").read_text(encoding="utf-8")
    m = re.search(r'BUNDLE_VERSION:\s*"([0-9]+\.[0-9]+\.[0-9]+)"', taskfile)
    if not m:
        sys.exit("staleness-check ABORT: BUNDLE_VERSION not found in Taskfile.yml")

    adr_nums = sorted(
        int(re.match(r"ADR-0*(\d+)", p.name).group(1))
        for p in (ROOT / "docs/adr").glob("ADR-*.md")
    )

    # Which ADRs are actually Draft, read from the files themselves.
    drafts = set()
    for p in (ROOT / "docs/adr").glob("ADR-*.md"):
        head = p.read_text(encoding="utf-8")[:600]
        if re.search(r"^\s*(##\s*Status\s*\n+)?\**Status:?\**\s*\n*\s*Draft\s*$",
                     head, re.M | re.I) or re.search(r"^Draft$", head, re.M):
            drafts.add(re.match(r"ADR-0*(\d+)", p.name).group(1).zfill(4))

    return {
        "release": m.group(1),
        "adr_head": adr_nums[-1],
        "adr_count": len(adr_nums),
        "drafts": drafts,
    }


def check(gt: dict) -> tuple[list, int]:
    fails, asserted = [], 0

    # --- 1. release currency -------------------------------------------------
    rel_re = re.compile(r"Latest release[:\s*]*\**v?([0-9]+\.[0-9]+\.[0-9]+)")
    for name in LIVE_DOCS:
        p = ROOT / name
        if not p.exists():
            continue
        for m in rel_re.finditer(p.read_text(encoding="utf-8")):
            asserted += 1
            if m.group(1) != gt["release"]:
                fails.append(
                    f"{name}: claims latest release v{m.group(1)}; "
                    f"Taskfile BUNDLE_VERSION is {gt['release']}"
                )

    # --- 2. ADR ladder head currency ----------------------------------------
    head_re = re.compile(r"(?:Current head|ADR ladder head)[:\s*]*\**ADR-0*(\d+)")
    for name in LIVE_DOCS:
        p = ROOT / name
        if not p.exists():
            continue
        for m in head_re.finditer(p.read_text(encoding="utf-8")):
            asserted += 1
            if int(m.group(1)) != gt["adr_head"]:
                fails.append(
                    f"{name}: claims ADR head {int(m.group(1)):04d}; "
                    f"highest ADR on disk is {gt['adr_head']:04d}"
                )

    # --- 3. ADR count claims ("ADRs (N+ accepted)") --------------------------
    cnt_re = re.compile(r"ADRs?\s*\((\d+)\+\s*accepted\)")
    for p in ROOT.rglob("*.md"):
        rel = str(p.relative_to(ROOT))
        if is_archival(rel) or "node_modules" in rel:
            continue
        for m in cnt_re.finditer(p.read_text(encoding="utf-8", errors="ignore")):
            asserted += 1
            claimed = int(m.group(1))
            # "N+" is a floor; it goes stale once the real count outruns it by a lot.
            if claimed + 10 < gt["adr_count"]:
                fails.append(
                    f"{rel}: claims 'ADRs ({claimed}+ accepted)'; "
                    f"{gt['adr_count']} ADRs exist on disk"
                )

    # --- 4. stale ADR-status claims -----------------------------------------
    # Any live doc calling an ADR "Draft" when its file is not Draft.
    draft_claim = re.compile(r"ADR-(\d{4})[^.\n]{0,60}?\bis still Draft\b|"
                             r"\bADR-(\d{4})\b[^.\n]{0,30}?\(Draft\)")
    for p in ROOT.rglob("*.md"):
        rel = str(p.relative_to(ROOT))
        if is_archival(rel) or "node_modules" in rel:
            continue
        text = p.read_text(encoding="utf-8", errors="ignore")
        for m in draft_claim.finditer(text):
            num = m.group(1) or m.group(2)
            asserted += 1
            if num not in gt["drafts"]:
                fails.append(
                    f"{rel}: describes ADR-{num} as Draft; that ADR is not Draft on disk"
                )

    # --- 5. spec-set stamp on live project files ----------------------------
    # SPEC-2026.1 is the identifier; v3.0.1 survives only in paths + filenames.
    stamp = re.compile(r"(?:Overlay|Instructions)[^\n]*\(AI Remote Engineering OS v3\.0\.1\)|"
                       r"^# [^\n]*— AI Remote Engineering OS v3\.0\.1$", re.M)
    for sub in ("ai", "claude-desktop"):
        for p in (ROOT / sub).rglob("*.md"):
            rel = str(p.relative_to(ROOT))
            asserted += 1
            if stamp.search(p.read_text(encoding="utf-8", errors="ignore")):
                fails.append(
                    f"{rel}: title stamped 'v3.0.1'; the spec-set identifier is "
                    f"SPEC-2026.1 (CANONICAL section-Version namespaces)"
                )

    return fails, asserted


def main() -> int:
    gt = ground_truth()
    fails, asserted = check(gt)
    if fails:
        print("staleness-check FAIL — canon claims contradicted by the tree:")
        for f in fails:
            print(f"  {f}")
        print(f"\nground truth: release={gt['release']} adr_head={gt['adr_head']:04d} "
              f"adr_count={gt['adr_count']} drafts={sorted(gt['drafts']) or 'none'}")
        return 1
    print(f"staleness-check OK — {asserted} self-descriptive claims match the tree "
          f"(release {gt['release']}, ADR head {gt['adr_head']:04d}, "
          f"{gt['adr_count']} ADRs, {len(gt['drafts'])} Draft)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
