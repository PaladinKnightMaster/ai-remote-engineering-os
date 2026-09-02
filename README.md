# AI Remote Engineering OS

A self-hosted, air-gap-capable **governance plane** for human and AI work on remote
machines: humans and AI agents operate internal hosts, run recurring workstreams,
share governed memory, and call approved tools — and every consequential action
lands on one signed, chained-hash evidence ledger that an outsider can verify
offline. [read: `docs/executive/vision.md`]

**The wedge, stated plainly:** this is not another cloud coding agent, another
remote-desktop tool, or another multi-agent chat UI. It is the place where
governance happens — policy, approval, credential custody, audit — not the place
where a model runs. [read: `docs/executive/vision.md`]

---

## What this governs

Every path an operator or an AI agent can use to touch a remote resource passes
through the same chain: **policy decision → (optional) human approval → a governed
connector that holds the only decryption key → an append-only, chained-hash audit
event.** Concretely, as of the latest shipped release (v0.15.0):

- **Remote Execution Cockpit** — a browser-based SSH terminal and SFTP session,
  audited per input line.
- **Typed MCP Gateway** — the surface an AI agent client connects to. It exposes a
  small, explicit tool set (`remote.exec`, `remote.file.read/write/list`,
  `run.launch`, `workstream.poll`, `plan.submit`, `memory.read`, `context.search`)
  gated by a fixed set of capability edges — no agent reaches a tool this catalog
  doesn't name. [read: `docs/security/agent-permission-model.md`]
- **Policy decisions** — an operator-authored rule store can return `allow`,
  `deny`, or `require-approval` for an action, before any human is asked
  (ADR-0074). An empty rule table changes nothing; rules govern the
  `(subject, edge, action, target)` tuple, not command arguments.
- **Human approval** — a park-and-resolve step an operator can approve or deny from
  the cockpit, for anything policy does not resolve on its own.
- **Governed connector** — the sole holder of target credentials; nothing else in
  the system can decrypt one.
- **Runs and Workstreams** — governed, multi-step execution that an operator can
  launch directly, delegate to an agent (bounded by a revocable grant), or charter
  to fire on a schedule.
- **Scoped memory and a live context index** — a governed, forgettable read plane
  an agent can query, always inside its own grant's scope; native Postgres
  full-text search, no embeddings.
- **A signed, chained-hash audit chain** — every governed event append-only,
  hash-linked, with signed chain heads anchored outside the database and an
  offline verifier that needs neither a live database nor a network connection.

Fifteen tagged releases (`v0.1.0` → `v0.15.0`) built this incrementally; see
`docs/releases/` for what shipped at each one.

---

## What this does **not** do

Stated plainly, because a security product with limitations discoverable only by
reading source is worse than one that says them up front.

- **It does not bundle a model runtime or any model weights.** A language model is
  entirely **operator-provided** — you point an MCP-speaking agent client at the
  gateway. This is an architectural decision, not a temporary gap: the smallest
  measured model (`gemma3:270m`, 278 MB compressed) is already **1.85×** the
  product's entire offline-bundle size cap on its own, before any runtime is added.
  [read: `docs/spike/inference-substrate-measurement.md`]
- **It does not yet integrate a governed software-engineering harness** (an
  OpenHands/openworker-style coding agent driving `remote.exec` end to end). That
  integration is **B-041**, the flagship of the *next* major release line
  (v1.0.0), and has **not been started**. [read: `docs/spike/b041-remaining-work.md`]
- **It does not constrain what an *approved* command does once it is running.** A
  policy rule can refuse to let a command *start*; nothing in the product today
  bounds what a started, approved command can do while it runs. That containment
  layer (**B-040**) is a deferred, separately-ADR'd piece of work, not a shipped
  feature. Today, governance over a running command is **cooperative, not
  enforced** — see `SECURITY.md` for the full statement of this limitation.
- **It is not a hosted service and does not have one.** No SaaS runtime dependency
  is a standing project rule; you run this on your own infrastructure, including
  fully air-gapped after installation.
- **It is not yet accepting external pull requests.** See `CONTRIBUTING.md`.

---

## Status

**Pre-1.0.** Fifteen releases have shipped and been tagged locally (`v0.1.0`
through `v0.15.0`); this release, **v0.16.0 "Public Readiness,"** is the
sixteenth, and its entire purpose is making the repository something a stranger
can install and verify without any of the internal context that produced it — this
is the first version of the README written for that reader.

The **v1.0.0** line is where the governed-harness integration (B-041, above) is
planned to land, along with an API surface freeze. Neither has happened yet.

---

## Install

The product ships as a signed, offline-installable bundle — no container registry,
no internet access required on the target host after the bundle is transferred.

1. **Build and sign the release artifacts** (on a connected host) —
   [`docs/devops/offline-install-v3.md`](docs/devops/offline-install-v3.md).
2. **Install on the target host** — the same document covers the offline install
   procedure. Supported today: **Ubuntu 24.04** and **Rocky/RHEL-family 9**,
   `amd64`. Windows is a browser client only; there is no Windows server build.
3. **Walk through your first governed action** —
   [`docs/devops/quickstart-first-governed-action.md`](docs/devops/quickstart-first-governed-action.md)
   takes you from a running install to a completed, audited action. Only the
   `lan-only` deployment profile is proven end to end today; the quickstart's own
   "Honest limits" section says so.

---

## License

**Apache License 2.0.** See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE). The choice
is a deliberate, reasoned decision — including the alternatives it rejected and
why — recorded in
[`docs/adr/ADR-0075-open-source-licence.md`](docs/adr/ADR-0075-open-source-licence.md).

## Security

See [`SECURITY.md`](SECURITY.md) for the disclosure channel and, just as
importantly, the product's known governance limitations stated in plain language.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Short version: sign off your commits
(DCO 1.1) — but this project is **not accepting external pull requests yet**.

---

## Documentation map

- [`docs/architecture/module-map.md`](docs/architecture/module-map.md) — what's
  built, service by service.
- [`docs/security/agent-permission-model.md`](docs/security/agent-permission-model.md)
  — the capability-edge model that gates every agent-reachable action.
- [`docs/releases/`](docs/releases/) — every release's own criteria, notes, and
  verification runbook, including this one.
- [`docs/adr/README.md`](docs/adr/README.md) — the generated ADR ledger; every
  binding architectural decision, indexed.
- `CANONICAL_SOURCE_OF_TRUTH.md` and `CLAUDE.md` — this project's own internal
  engineering-process documents (written for its AI-assisted development
  workflow). Useful if you want to see how the project runs itself; not required
  reading to install or use the product.
