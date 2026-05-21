#!/usr/bin/env python3
"""Validate the canonical contracts in CI (T-010).

Two checks, no npm dependency (offline-first; the first npm/pnpm lockfile
precedent is deferred to the frontend ticket T-007):

1. JSON Schema fixtures — every entry in packages/shared-schemas/test/fixtures/
   valid.json must validate against its schema (keyed by schema `title`); every
   entry in invalid.json must FAIL. This is the executable surface T-003 and
   T-009 deferred to T-010.
2. OpenAPI fragment — structural checks on docs/api-contracts/openapi.v3.fragment.yaml:
   3.1.0, the audit paths + AuditEventIngest exist, the ingest DTO rejects
   client-supplied chain fields, and every internal $ref resolves.

Exit non-zero on any failure. Requires: pyyaml, jsonschema.
"""
from __future__ import annotations

import json
import pathlib
import re
import sys

import yaml
from jsonschema import Draft202012Validator

ROOT = pathlib.Path(__file__).resolve().parents[2]
SCHEMA_DIR = ROOT / "packages" / "shared-schemas"
FIXTURES = SCHEMA_DIR / "test" / "fixtures"
OPENAPI = ROOT / "docs" / "api-contracts" / "openapi.v3.fragment.yaml"

failures: list[str] = []


def fail(msg: str) -> None:
    failures.append(msg)
    print(f"  FAIL: {msg}")


def schemas_by_title() -> dict[str, dict]:
    out: dict[str, dict] = {}
    for path in SCHEMA_DIR.glob("*.schema.json"):
        doc = json.loads(path.read_text(encoding="utf-8"))
        title = doc.get("title")
        if title:
            out[title] = doc
    return out


def check_fixtures() -> None:
    print("== JSON Schema fixtures ==")
    titles = schemas_by_title()
    valid = json.loads((FIXTURES / "valid.json").read_text(encoding="utf-8"))
    invalid = json.loads((FIXTURES / "invalid.json").read_text(encoding="utf-8"))

    for title, instance in valid.items():
        schema = titles.get(title)
        if schema is None:
            fail(f"valid fixture '{title}' has no matching schema title")
            continue
        errs = list(Draft202012Validator(schema).iter_errors(instance))
        if errs:
            fail(f"valid fixture '{title}' did not validate: {errs[0].message}")
        else:
            print(f"  ok: valid '{title}' validates")

    for title, instance in invalid.items():
        schema = titles.get(title)
        if schema is None:
            fail(f"invalid fixture '{title}' has no matching schema title")
            continue
        if Draft202012Validator(schema).is_valid(instance):
            fail(f"invalid fixture '{title}' unexpectedly validated (should fail)")
        else:
            print(f"  ok: invalid '{title}' correctly rejected")


def check_openapi() -> None:
    print("== OpenAPI fragment ==")
    d = yaml.safe_load(OPENAPI.read_text(encoding="utf-8"))
    if d.get("openapi") != "3.1.0":
        fail(f"openapi version is {d.get('openapi')!r}, want 3.1.0")
    paths = d.get("paths", {})
    for p in ("/v1/events", "/v1/events/{id}"):
        if p not in paths:
            fail(f"missing path {p}")
    schemas = d.get("components", {}).get("schemas", {})
    for s in ("Resource", "Session", "AuditEvent", "Error", "PageMeta", "AuditEventIngest"):
        if s not in schemas:
            fail(f"missing component schema {s}")
    ingest = schemas.get("AuditEventIngest", {})
    if ingest.get("additionalProperties") is not False:
        fail("AuditEventIngest must set additionalProperties: false")
    for forbidden in ("prevHash", "chainHash", "id"):
        if forbidden in ingest.get("properties", {}):
            fail(f"AuditEventIngest must not expose chain field {forbidden}")
    txt = json.dumps(d)
    for sect, name in set(re.findall(r"#/components/([a-zA-Z]+)/([a-zA-Z0-9]+)", txt)):
        if name not in d.get("components", {}).get(sect, {}):
            fail(f"dangling $ref #/components/{sect}/{name}")
    if not failures:
        print("  ok: structure, audit surface, ingest DTO, and refs all valid")


def main() -> int:
    check_fixtures()
    check_openapi()
    if failures:
        print(f"\n{len(failures)} contract check(s) failed.")
        return 1
    print("\nAll contract checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
