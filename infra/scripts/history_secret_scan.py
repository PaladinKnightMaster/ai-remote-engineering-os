#!/usr/bin/env python3
"""history-secret-scan (release-criteria PUB-1, SEC-60; T-157).

Content-level secret scan over the FULL git history -- every commit reachable
from every ref (`git rev-list --objects --all`), not the working tree and not
filenames. The only prior instrument (the v0.16 scoping sweep) checked
filenames only and would not have caught a credential pasted INSIDE a file;
this closes that gap. **Once history is pushed it cannot be unpushed** (§9 of
the v0.16.0 criteria) -- this is the one irreversible gate in the release.

Method: enumerate every blob reachable from any ref via `git rev-list
--objects --all` piped (via subprocess, not a shell pipe) into `git cat-file
--batch-check` to get type + size, then fetch content for blobs under
MAX_BLOB_BYTES in one `git cat-file --batch` stream. This dedupes by blob
CONTENT (a blob referenced by 50 commits is scanned once), which is both
faster and the correct unit for "was this content ever committed" -- a
commit-diff-only scan (`git log -p`) can under-count content introduced only
via a merge resolution. Binaries/large blobs (release tarballs, images) are
skipped by size; secrets pasted as text do not hide inside a 40MB .tar.gz in
this repo's own history, and scanning them would make a full-history run
impractically slow for no coverage gain.

Rules cover: PEM/OpenSSH private key blocks, AWS-style access key IDs,
credentials embedded in connection-string URLs, bearer tokens, JWT literals,
PASETO v2/v4 literals, .env-style SECRET/PASSWORD/TOKEN/API_KEY/ACCESS_KEY
assignments, and high-entropy quoted values on a line whose context names a
secret. None of these require the working tree to build or run -- pure
stdlib, so this stays TOOLING and never enters the Go/JS dependency surface
`go-deps-check` / `task licenses` govern (CLAUDE.md §Verification discipline).

ALLOWLIST is by exact repo-relative path only (never a glob/prefix) -- each
entry is a claim that one specific file's content is safe, and a glob would
let a genuine secret shelter next to an allowlisted path undetected.
"""
import math
import re
import subprocess
import sys
import pathlib
from collections import defaultdict

ROOT = pathlib.Path(__file__).resolve().parents[2]  # infra/scripts/ -> repo root
MAX_BLOB_BYTES = 2_000_000

# Exact repo-relative path -> justification. A path here is a claim that its
# content is a KNOWN, INTENTIONAL fixture, never a real credential. No globs:
# a broad pattern would let a genuine secret shelter next to an allowlisted
# file undetected.
ALLOWLIST = {
    "infra/offline/testdata/fixture-release-key/FIXTURE-DO-NOT-USE-release.key.pem": (
        "Test-only PEM consumed by `task sec45-check` / offline-bundle "
        "signing tests (Taskfile.yml sec45-check task, ADR-0070 D1/D2/D9). "
        "Filename is self-labelled FIXTURE-DO-NOT-USE; found at v0.16 "
        "scoping by the filename-only sweep and confirmed intentional then; "
        "not a credential for any live system."
    ),
    "services/connector-service/internal/store/store_integration_test.go": (
        "Fixture Material{PrivateKey: \"...BEGIN OPENSSH PRIVATE KEY...abc...\", "
        "Passphrase: \"hunter2\"} -- inspected T-157: the key body is the literal "
        "string 'abc' (not a parseable key) and the passphrase is the well-known "
        "bash.org placeholder joke; a round-trip fixture for store serialisation, "
        "not connector key material."
    ),
    "services/api/internal/secret/secret_integration_test.go": (
        "Same fixture literal as the connector-service test above (identical "
        "'abc' key body / 'hunter2' passphrase) -- inspected T-157."
    ),
    "services/audit-service/internal/chain/head_test.go": (
        "authctlFixtureSecretHex/PublicHex: a keypair the file's own comment "
        "says was produced by `authctl genkeypair` solely to test "
        "SecretKeyFromHex/PublicKeyFromHex parsing -- inspected T-157, no "
        "chain-signing use."
    ),
    "infra/docker-compose/docker-compose.e2e.yml": (
        "AIREOS_RESOURCE_SECRET_KEY: e2e-resource-secret-not-the-paseto-key -- "
        "the value names itself as a placeholder; e2e-only compose env, never "
        "deployed."
    ),
    "infra/docker-compose/env.example": (
        "A `.env.example` template by definition holds placeholders, not live "
        "values -- every credential in it is the literal word 'change-me' "
        "(inspected T-157)."
    ),
}

RULES = [
    ("private_key_block", re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----")),
    ("aws_access_key_id", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("conn_string_credential", re.compile(r"\b\w{2,10}://[^\s:'\"@/]+:[^\s@'\"]{3,}@[^\s'\"/]+")),
    ("bearer_token", re.compile(r"(?i)\bbearer\s+[a-z0-9\-_.=]{20,}")),
    ("jwt_literal", re.compile(r"\beyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\b")),
    ("paseto_literal", re.compile(r"\bv[24]\.(?:local|public)\.[a-zA-Z0-9_-]{20,}\b")),
    ("dotenv_secret_assignment", re.compile(
        r"(?im)^[ \t]*[A-Za-z0-9_]*(?:SECRET|PASSWORD|PASSWD|TOKEN|API_KEY|ACCESS_KEY|PRIVATE_KEY)"
        r"[A-Za-z0-9_]*[ \t]*[:=][ \t]*['\"]?[A-Za-z0-9/+_.\-]{12,}['\"]?"
    )),
]
ENTROPY_CONTEXT = re.compile(r"(?i)(secret|token|password|api[_-]?key|access[_-]?key)")
ENTROPY_VALUE = re.compile(r"['\"]([A-Za-z0-9+/=_\-]{20,})['\"]")


def shannon_entropy(s):
    freq = defaultdict(int)
    for c in s:
        freq[c] += 1
    n = len(s)
    return -sum((c / n) * math.log2(c / n) for c in freq.values())


def redact(s, keep=4):
    if len(s) <= keep * 2:
        return "*" * len(s)
    return s[:keep] + "...redacted..." + s[-keep:]


def run(args, input_text=None):
    return subprocess.run(
        args, cwd=ROOT, capture_output=True, text=True,
        input=input_text, encoding="utf-8", errors="replace",
    )


def list_blobs():
    objs = run(["git", "rev-list", "--objects", "--all"])
    if objs.returncode != 0:
        sys.exit(f"history-secret-scan: git rev-list failed: {objs.stderr}")
    shas, paths = [], defaultdict(set)
    for line in objs.stdout.splitlines():
        parts = line.split(" ", 1)
        shas.append(parts[0])
        if len(parts) == 2 and parts[1]:
            paths[parts[0]].add(parts[1])
    if not shas:
        sys.exit("history-secret-scan: git rev-list returned zero objects -- refusing to report a clean scan.")
    check = run(["git", "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)"],
                input_text="\n".join(shas) + "\n")
    if check.returncode != 0:
        sys.exit(f"history-secret-scan: git cat-file --batch-check failed: {check.stderr}")
    blobs = []
    for line in check.stdout.splitlines():
        f = line.split(" ")
        if len(f) == 3 and f[1] == "blob" and int(f[2]) <= MAX_BLOB_BYTES:
            blobs.append(f[0])
    return shas, blobs, paths


def read_blobs(blobs):
    """Stream every blob's raw bytes out of ONE `git cat-file --batch` process."""
    proc = subprocess.Popen(["git", "cat-file", "--batch"], cwd=ROOT,
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    out, _ = proc.communicate(input=("\n".join(blobs) + "\n").encode("utf-8"))
    if proc.returncode != 0:
        sys.exit("history-secret-scan: git cat-file --batch failed")
    contents, i, n = {}, 0, len(out)
    while i < n:
        nl = out.index(b"\n", i)
        sha, _typ, size = out[i:nl].decode("utf-8").split(" ")
        size = int(size)
        start = nl + 1
        contents[sha] = out[start:start + size]
        i = start + size + 1  # skip the object's trailing newline
    return contents


def find_commits(path, needle):
    r = run(["git", "log", "--all", "--format=%H", f"-S{needle}", "--", path])
    return r.stdout.split()[:5] if r.returncode == 0 else []


def main():
    shas, blobs, paths = list_blobs()
    contents = read_blobs(blobs)
    findings, seen = [], set()
    for sha, data in contents.items():
        try:
            text = data.decode("utf-8")
        except UnicodeDecodeError:
            text = data.decode("latin-1")
        blob_paths = sorted(paths.get(sha, {"<unknown path>"}))
        if all(p in ALLOWLIST for p in blob_paths):
            continue
        for rule_name, pattern in RULES:
            m = pattern.search(text)
            if m and (rule_name, sha) not in seen:
                seen.add((rule_name, sha))
                findings.append((rule_name, sha, blob_paths, m.group(0)))
        for m in ENTROPY_VALUE.finditer(text):
            line_start = text.rfind("\n", 0, m.start()) + 1
            line_end = text.find("\n", m.end())
            line = text[line_start: line_end if line_end != -1 else len(text)]
            key = ("high_entropy_in_secret_context", sha)
            if key not in seen and ENTROPY_CONTEXT.search(line) and shannon_entropy(m.group(1)) >= 4.0:
                seen.add(key)
                findings.append((*key, blob_paths, m.group(1)))

    print(f"history-secret-scan: {len(shas)} objects enumerated (git rev-list --objects --all), "
          f"{len(blobs)} blobs <= {MAX_BLOB_BYTES}B fetched and scanned as text, "
          f"{len(ALLOWLIST)} allowlist path(s) applied, {len(RULES)} pattern rules + entropy rule.")
    if not findings:
        print("history-secret-scan: NO FINDINGS.")
        return 0

    for rule_name, sha, blob_paths, match in findings:
        commits = find_commits(blob_paths[0], match[:40]) if blob_paths[0] != "<unknown path>" else []
        print(f"FINDING rule={rule_name} blob={sha[:12]} path={blob_paths[0]!r} "
              f"redacted={redact(match)!r} commits={commits}")
    print(f"history-secret-scan: {len(findings)} distinct (rule, blob) finding(s) -- see FINDING lines above.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
