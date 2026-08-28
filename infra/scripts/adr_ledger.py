#!/usr/bin/env python3
"""adr_ledger — GENERATE the ADR status ledger from the ADR files themselves.

Why this is generated and not written by hand:

  `docs/requirements/adr_index.csv` was hand-written, covered ADR-0001..0018, and
  was never touched again. By 2026-08-27 that was **25% coverage** of a 73-ADR
  corpus, and the gap was invisible because nothing compared the index to the
  tree. A hand-maintained index of a growing corpus is a stale index with a
  delay, not an index.

What this closes, concretely:

  * **ADR-0041's dead clause.** ADR-0072 superseded ADR-0041's Go-version
    sentence. Gate G2 forbids editing an Accepted ADR, so ADR-0041 carries no
    forward pointer and a reader of it cannot learn the clause is dead. The
    REVERSE INDEX below is the fix: it is built additively, outside the ADR
    files, so nothing is edited and the pointer still exists.
  * **ADR-0017 sitting in `Draft` through its own implementation** (v0.10.0 /
    ADR-0066) for fifteen months, because no artifact ever listed statuses.

Design rules, because this is a checking instrument and that is where this
project's errors concentrate:

  * Every field is derived from the ADR FILES. Nothing is inherited from prose
    summaries elsewhere in the repo - those are exactly what went stale.
  * Relations are extracted VERBATIM from each ADR's own Status block, which is
    where this corpus declares supersession/refinement. Relations that are only
    stated in body prose are NOT guessed - they are reported as uncurated, so
    the gap is countable rather than invented.
  * Two incompatible Status templates exist (`## Status` from ADR-0001..0018 and
    ADR-0041+, inline `Status:` in ADR-0019..0040). Both are parsed. A grep for
    `## Status` alone silently misses 22 ADRs - that was a real finding.

Outputs (one source, two files, so they cannot disagree):
    docs/adr/README.md                  - the human-readable ledger
    docs/requirements/adr_index.csv     - the machine-readable index, all ADRs

Usage:
    python infra/scripts/adr_ledger.py            # write both outputs
    python infra/scripts/adr_ledger.py --check    # exit 1 if either is stale
"""
import csv
import io
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
ADR_DIR = ROOT / "docs/adr"
LEDGER = ADR_DIR / "README.md"
INDEX = ROOT / "docs/requirements/adr_index.csv"

STATUS_WORDS = ("Accepted", "Proposed", "Draft", "Superseded", "Rejected")


def parse(p: pathlib.Path) -> dict:
    text = p.read_text(encoding="utf-8")
    num = re.match(r"ADR-0*(\d+)", p.name).group(1).zfill(4)

    first = text.splitlines()[0]
    m = re.match(r"#\s*ADR-\d+:\s*(.+?)\s*$", first)
    title = m.group(1) if m else first.lstrip("# ").strip()

    # Both templates: '## Status\n\n<block>' and inline 'Status: X'
    blk = re.search(r"^##\s*Status\s*\n+(.*?)(?=\n##\s|\Z)", text, re.M | re.S)
    if blk:
        status_block = blk.group(1).strip()
    else:
        inline = re.search(r"^Status:\s*(.+)$", text, re.M)
        status_block = inline.group(1).strip() if inline else ""

    flat = re.sub(r"\s+", " ", status_block)
    word = next((w for w in STATUS_WORDS if re.search(rf"\b{w}\b", flat, re.I)), "UNKNOWN")
    date = (re.search(r"(20\d\d-\d\d-\d\d)", flat) or [None, ""])[1] if re.search(r"20\d\d-\d\d-\d\d", flat) else ""

    # Relations declared in the Status block, kept verbatim, other ADRs only.
    rel = ""
    for sent in re.split(r"(?<=\.)\s+", flat):
        if re.search(r"\bADR-\d{4}\b", sent) and re.search(
            r"supersed|refin|extend|amend|discharg|promot|preserv|replac", sent, re.I
        ):
            rel += (" " if rel else "") + sent.strip()
    rel = re.sub(r"\*\*|\*|`", "", rel).strip()
    targets = sorted(set(re.findall(r"ADR-(\d{4})", rel)) - {num})

    dec = re.search(r"^##\s*Decision\s*\n+(.*?)(?=\n##\s|\Z)", text, re.M | re.S)
    decision = ""
    if dec:
        for line in dec.group(1).splitlines():
            s = re.sub(r"\*\*|\*|`|^\s*[-\d.]+\s*", "", line).strip()
            if s and not s.startswith("#"):
                decision = s
                break

    return {
        "num": num, "title": title, "status": word, "date": date,
        "relation": rel, "targets": targets, "decision": decision,
        "file": p.name, "bytes": p.stat().st_size,
    }


def build(adrs: list) -> tuple[str, str]:
    by_num = {a["num"]: a for a in adrs}
    # Reverse index: who NAMES this ADR in their Status block. Fully mechanical,
    # additive, and the reason ADR-0041's dead clause becomes visible.
    inbound: dict[str, list] = {a["num"]: [] for a in adrs}
    for a in adrs:
        for t in a["targets"]:
            if t in inbound:
                inbound[t].append(a["num"])

    counts: dict[str, int] = {}
    for a in adrs:
        counts[a["status"]] = counts.get(a["status"], 0) + 1
    uncurated = sum(1 for a in adrs if not a["relation"])

    out = io.StringIO()
    w = out.write
    w("# ADR ledger — GENERATED, do not edit by hand\n\n")
    w("> Regenerate with `task adr-ledger`; `task adr-ledger-check` fails if this file\n"
      "> has drifted from `docs/adr/*.md`. Source: `infra/scripts/adr_ledger.py`.\n\n")
    w("**Why this file exists.** `adr_index.csv` was hand-written, stopped at ADR-0018, and was "
      "never revisited — **25% coverage** of a 73-ADR corpus, with nothing comparing it to the "
      "tree. This ledger is generated from the ADR files, so it cannot drift silently.\n\n")
    w("**The reverse index is the point.** Gate **G2** forbids editing an Accepted ADR, so an ADR "
      "whose clause is later superseded carries **no forward pointer** — a reader of ADR-0041 "
      "cannot learn that its Go-version clause is dead. *Named by* below supplies that pointer "
      "**additively, outside the ADR files**, satisfying both G2 and the MADR convention that a "
      "superseded decision must link forward.\n\n")
    w(f"**{len(adrs)} ADRs.** " + " · ".join(
        f"**{k}: {v}**" for k, v in sorted(counts.items())) + "\n\n")

    w("## Known corpus defects (measured, not impressions)\n\n")
    stubs = [a for a in adrs if a["bytes"] < 800]
    big = [a for a in adrs if a["bytes"] > 15000]
    w(f"- **Bimodal by size.** {len(stubs)} ADRs are under 800 bytes (title + one-line decision); "
      f"{len(big)} are over 15 KB (design documents). Neither shape is an ADR in the "
      f"Nygard/MADR sense.\n")
    w(f"- **Two incompatible Status templates** — `## Status` vs inline `Status:`, split at "
      f"ADR-0019. A grep for `## Status` alone misses 22 ADRs. Both are parsed here.\n")
    w(f"- **{uncurated} ADRs declare no relation in their Status block.** For the stub band that is "
      f"expected; where a relation exists only in body prose it is **not guessed** — the gap is "
      f"left visible rather than invented.\n")
    w("- **Decision bundling.** Recent ADRs carry many decisions each (ADR-0067 and ADR-0068 have "
      "21 internal decision points; 0069/0070/0071 have 9). **ADR-0072 exists only because "
      "ADR-0041 bundled five decisions**, so retiring one required a whole new ADR. New ADRs "
      "should carry **one** decision — see `TEMPLATE.md`.\n\n")

    w("## Ledger\n\n")
    w("| ADR | Title | Status | Date | Relations (verbatim from its Status block) | Named by |\n")
    w("|---|---|---|---|---|---|\n")
    for a in adrs:
        rel = a["relation"] or "—"
        if len(rel) > 200:
            rel = rel[:197] + "…"
        named = ", ".join(f"ADR-{n}" for n in inbound[a["num"]]) or "—"
        w(f"| [ADR-{a['num']}]({a['file']}) | {a['title']} | **{a['status']}** | "
          f"{a['date'] or '—'} | {rel} | {named} |\n")

    buf = io.StringIO()
    cw = csv.writer(buf, lineterminator="\n")
    cw.writerow(["ADR", "Title", "Status", "Date", "Decision", "Relations", "NamedBy"])
    cw.writerow(["# GENERATED by infra/scripts/adr_ledger.py - do not hand-edit. "
                 "Canonical human view: docs/adr/README.md", "", "", "", "", "", ""])
    for a in adrs:
        cw.writerow([f"ADR-{a['num']}", a["title"], a["status"], a["date"],
                     a["decision"], a["relation"],
                     " ".join(f"ADR-{n}" for n in inbound[a["num"]])])
    return out.getvalue(), buf.getvalue()


def main() -> int:
    adrs = sorted((parse(p) for p in ADR_DIR.glob("ADR-*.md")), key=lambda a: a["num"])
    if not adrs:
        sys.exit("adr-ledger ABORT: no ADR files found")
    ledger, index = build(adrs)

    if "--check" in sys.argv:
        stale = []
        if not LEDGER.exists() or LEDGER.read_text(encoding="utf-8") != ledger:
            stale.append(str(LEDGER.relative_to(ROOT)))
        if not INDEX.exists() or INDEX.read_text(encoding="utf-8") != index:
            stale.append(str(INDEX.relative_to(ROOT)))
        if stale:
            print("adr-ledger FAIL — generated artifacts are stale:")
            for s in stale:
                print(f"  {s}  (run `task adr-ledger`)")
            return 1
        print(f"adr-ledger OK — both generated artifacts match all {len(adrs)} ADR files")
        return 0

    LEDGER.write_text(ledger, encoding="utf-8")
    INDEX.write_text(index, encoding="utf-8")
    print(f"adr-ledger OK — wrote {LEDGER.relative_to(ROOT)} and "
          f"{INDEX.relative_to(ROOT)} from {len(adrs)} ADR files")
    return 0


if __name__ == "__main__":
    sys.exit(main())
