# ADR ledger — GENERATED, do not edit by hand

> Regenerate with `task adr-ledger`; `task adr-ledger-check` fails if this file
> has drifted from `docs/adr/*.md`. Source: `infra/scripts/adr_ledger.py`.

**Why this file exists.** `adr_index.csv` was hand-written, stopped at ADR-0018, and was never revisited — **25% coverage** of a 73-ADR corpus, with nothing comparing it to the tree. This ledger is generated from the ADR files, so it cannot drift silently.

**The reverse index is the point.** Gate **G2** forbids editing an Accepted ADR, so an ADR whose clause is later superseded carries **no forward pointer** — a reader of ADR-0041 cannot learn that its Go-version clause is dead. *Named by* below supplies that pointer **additively, outside the ADR files**, satisfying both G2 and the MADR convention that a superseded decision must link forward.

**79 ADRs.** **Accepted: 71** · **Proposed: 8**

## Known corpus defects (measured, not impressions)

- **Bimodal by size.** 37 ADRs are under 800 bytes (title + one-line decision); 16 are over 15 KB (design documents). Neither shape is an ADR in the Nygard/MADR sense.
- **Two incompatible Status templates** — `## Status` vs inline `Status:`, split at ADR-0019. A grep for `## Status` alone misses 22 ADRs. Both are parsed here.
- **55 ADRs declare no relation in their Status block.** For the stub band that is expected; where a relation exists only in body prose it is **not guessed** — the gap is left visible rather than invented.
- **Decision bundling.** Recent ADRs carry many decisions each (ADR-0067 and ADR-0068 have 21 internal decision points; 0069/0070/0071 have 9). **ADR-0072 exists only because ADR-0041 bundled five decisions**, so retiring one required a whole new ADR. New ADRs should carry **one** decision — see `TEMPLATE.md`.

## Ledger

| ADR | Title | Status | Date | Relations (verbatim from its Status block) | Named by |
|---|---|---|---|---|---|
| [ADR-0001](ADR-0001.md) | Self-hosted/offline-first is a non-negotiable product constraint | **Accepted** | — | — | — |
| [ADR-0002](ADR-0002.md) | Split product into Remote Cockpit, Agentic Desktop, and Agentic Work Board | **Accepted** | — | — | ADR-0073 |
| [ADR-0003](ADR-0003.md) | Treat board/ticket as the control plane for agent work | **Accepted** | — | — | — |
| [ADR-0004](ADR-0004.md) | Use existing remote desktop tools before building custom WebRTC host agent | **Accepted** | — | — | — |
| [ADR-0005](ADR-0005.md) | Support both VPN-first and DMZ-first Topology C deployment | **Accepted** | — | — | ADR-0068 |
| [ADR-0006](ADR-0006.md) | Use QR for short-lived pairing, not credential transfer | **Accepted** | — | — | — |
| [ADR-0007](ADR-0007.md) | Use model gateway abstraction for AI providers | **Accepted** | — | — | — |
| [ADR-0008](ADR-0008.md) | Approval-first AI tool execution | **Accepted** | — | — | — |
| [ADR-0009](ADR-0009.md) | Knowledge base and code forge are future platform modules | **Accepted** | 2026-08-27 | — | — |
| [ADR-0010](ADR-0010.md) | Integrate existing Git forges before building native forge | **Accepted** | 2026-08-27 | — | — |
| [ADR-0011](ADR-0011.md) | Use private reviewed skill registry | **Accepted** | — | — | — |
| [ADR-0012](ADR-0012.md) | Every important action is an event | **Accepted** | — | — | — |
| [ADR-0013](ADR-0013.md) | Offline release bundle is part of product quality gate | **Accepted** | — | — | — |
| [ADR-0014](ADR-0014.md) | Postpone full autonomous GUI computer-use | **Accepted** | — | — | — |
| [ADR-0015](ADR-0015.md) | Use adapters for agent runtimes | **Accepted** | — | — | — |
| [ADR-0016](ADR-0016.md) | MVP board is agent-native, not full Jira clone | **Accepted** | — | — | — |
| [ADR-0017](ADR-0017.md) | Docs/knowledge base must serve humans and agents | **Accepted** | 2026-08-27 | Draft for 15 months; its agent-facing half was in fact DISCHARGED by ADR-0066 at v0.10.0 while this ADR still read Draft. | — |
| [ADR-0018](ADR-0018.md) | Security is product surface, not backend-only concern | **Accepted** | — | — | — |
| [ADR-0019](ADR-0019.md) | Make Agent Harness Runtime a first-class component | **Accepted** | — | — | — |
| [ADR-0020](ADR-0020.md) | Use filesystem-style inspectable memory plus metadata/indexes | **Accepted** | — | — | — |
| [ADR-0021](ADR-0021.md) | Use model gateway instead of direct model coupling | **Accepted** | — | — | — |
| [ADR-0022](ADR-0022.md) | Add Agent Egress Gateway for secrets and API governance | **Accepted** | — | — | — |
| [ADR-0023](ADR-0023.md) | Use tickets as executable work contracts | **Accepted** | — | — | — |
| [ADR-0024](ADR-0024.md) | Add context budget manager for all agent runs | **Accepted** | — | — | — |
| [ADR-0025](ADR-0025.md) | Treat code intelligence as a shared service | **Accepted** | — | — | — |
| [ADR-0026](ADR-0026.md) | Keep AI buddy/avatar optional and post-MVP | **Accepted** | — | — | — |
| [ADR-0027](ADR-0027.md) | Support local AI setup profile with LM Studio/Ollama/Continue | **Accepted** | — | — | — |
| [ADR-0028](ADR-0028.md) | Use capability edges as unified communication and authorization primitive | **Accepted** | — | — | — |
| [ADR-0029](ADR-0029.md) | Adopt Living Workstream Engine | **Accepted** | — | — | — |
| [ADR-0030](ADR-0030.md) | Adopt Agent Workflow Compiler | **Proposed** | — | — | — |
| [ADR-0031](ADR-0031.md) | Adopt Typed MCP Gateway | **Accepted** | — | — | — |
| [ADR-0032](ADR-0032.md) | Desktop Automation Prioritizes Accessibility Trees | **Accepted** | — | — | — |
| [ADR-0033](ADR-0033.md) | Adopt Continuous Context Index | **Proposed** | — | — | — |
| [ADR-0034](ADR-0034.md) | Adopt Persistent Inspectable Memory | **Proposed** | — | — | — |
| [ADR-0035](ADR-0035.md) | Adopt Git and Issue Intelligence Cache | **Proposed** | — | — | — |
| [ADR-0036](ADR-0036.md) | Adopt Evaluation Layer | **Proposed** | — | — | — |
| [ADR-0037](ADR-0037.md) | Adopt Agent Backend Sandbox | **Proposed** | — | — | — |
| [ADR-0038](ADR-0038.md) | Adopt Domain Pack Architecture | **Proposed** | — | — | — |
| [ADR-0039](ADR-0039.md) | Keep Companion UX Optional | **Accepted** | — | — | — |
| [ADR-0040](ADR-0040.md) | Adopt Oracle Escalation Profiles | **Proposed** | — | — | — |
| [ADR-0041](ADR-0041.md) | Go as primary backend language; TypeScript for web frontend; Python as sanctioned AI sidecar | **Accepted** | 2026-05-16 | — | ADR-0072 |
| [ADR-0042](ADR-0042.md) | Monorepo tooling — Go workspaces + pnpm workspaces, orchestrated by a top-level Taskfile | **Accepted** | 2026-05-16 | — | — |
| [ADR-0043](ADR-0043.md) | Go API framework — `go-chi/chi` on top of `net/http`, with `oapi-codegen` for schema-first server stubs | **Accepted** | 2026-05-17 | — | ADR-0067 |
| [ADR-0044](ADR-0044.md) | DB migration tool — `golang-migrate/migrate` in library mode, embedded per service, pure-SQL files | **Accepted** | 2026-05-17 | — | ADR-0067 |
| [ADR-0045](ADR-0045.md) | Event bus at P0 — no broker; Postgres transactional outbox with sync HTTP ingest into `audit-service`; NATS JetStream held as the P1 reserve pick | **Accepted** | 2026-05-17 | — | ADR-0064, ADR-0067 |
| [ADR-0046](ADR-0046.md) | Auth adapter at P0 — no IdP daemon; in-process `packages/go/auth/` issuing PASETO v4 capability tokens; Zitadel held as the P1 reserve pick | **Accepted** | 2026-05-18 | — | ADR-0050, ADR-0060, ADR-0062, ADR-0064, ADR-0065, ADR-0066, ADR-0068, ADR-0078 |
| [ADR-0047](ADR-0047.md) | Frontend framework for `apps/web` — no meta-framework at P0; plain Vite 6 + React 18 SPA served as static assets; Remix (SPA mode) held as the P1 reserve pick | **Accepted** | 2026-05-19 | — | ADR-0068 |
| [ADR-0048](ADR-0048.md) | Cockpit session lifecycle — in-process Go SSH for terminal, services/api for control plane, per-input-line audit, pgcrypto for target secrets | **Accepted** | 2026-05-28 | — | ADR-0049, ADR-0050, ADR-0058 |
| [ADR-0049](ADR-0049.md) | Cockpit session audit content model (hash-in-chain + cast-anchored), broker-authoritative close, and v0.1.0 reconnect deferral | **Accepted** | 2026-05-31 | Refines ADR-0048 §6 (audit granularity) and §8 (TTL / reconnect). Does not modify ADR-0048; this is the new-ADR record of decisions the SSH-slice implementation (T-016…T-020) made that ADR-0048's t… | ADR-0060, ADR-0064, ADR-0067 |
| [ADR-0050](ADR-0050-cockpit-ws-data-plane-contract.md) | Cockpit WebSocket data-plane contract — channel path and operator-audience cookie auth | **Accepted** | 2026-05-31 | Refines ADR-0048 §1 (terminal transport / WS path) and §4 (WebSocket-upgrade authentication), and ADR-0046 (token audience model) as it applies to the cockpit WebSocket. Does not modify those ADRs;… | ADR-0060 |
| [ADR-0051](ADR-0051-offline-install-architecture.md) | Offline-install architecture — Linux static-binary host, Caddy proxy, host-provided postgres with staged offline packages, dual operator provisioning, self-signed TLS | **Accepted** | 2026-05-31 | — | ADR-0052, ADR-0068, ADR-0070 |
| [ADR-0052](ADR-0052-postgres-companion-artifacts.md) | Offline postgres ships as per-OS companion artifacts (not in the core bundle) | **Accepted** | 2026-05-31 | Refines ADR-0051 decision 3. ADR-0051 said the offline bundle "ships staged install packages (.deb/.rpm) + a guide" — i.e. | ADR-0070, ADR-0077 |
| [ADR-0053](ADR-0053-connector-service.md) | connector-service — credential custody + egress gateway (promotes the ADR-0048 §7 reserve and closes the SEC-12 pgcrypto debt) | **Accepted** | 2026-06-01 | — | ADR-0068, ADR-0071, ADR-0079 |
| [ADR-0054](ADR-0054-approval-policy-decision-point.md) | policy-service approval/policy decision point + `<ApprovalPrompt>` (the human-in-the-loop primitive) | **Accepted** | 2026-06-01 | — | ADR-0074 |
| [ADR-0055](ADR-0055-v0.2.0-durability-rider-rescope.md) | v0.2.0 criteria amendment — the durability riders re-scoped (WD-2.5 / FE-9) | **Accepted** | 2026-06-11 | — | — |
| [ADR-0056](ADR-0056-mcp-gateway-protocol-surface.md) | Typed MCP Gateway protocol surface & trust boundary (`services/mcp-gateway`, the first agent surface) | **Accepted** | 2026-06-14 | — | ADR-0060, ADR-0065, ADR-0066 |
| [ADR-0057](ADR-0057-tool-catalog-invocation-governance.md) | Typed tool-catalog schema, tool→capability-edge mapping & invocation-anchored governance | **Accepted** | 2026-06-14 | — | ADR-0059, ADR-0060, ADR-0061, ADR-0071, ADR-0074 |
| [ADR-0058](ADR-0058-broker-noninteractive-exec.md) | Broker non-interactive exec egress + audit content model | **Accepted** | 2026-06-14 | No ADR divergence (G2-clean): it extends ADR-0048/0049 with a new egress modality; it edits no Accepted ADR. | ADR-0071 |
| [ADR-0059](ADR-0059-informed-invocation-approval.md) | Informed invocation approval — command visibility & parked-invocation lifecycle | **Accepted** | 2026-06-15 | No ADR divergence (G2-clean): it extends ADR-0057's approval surface and edits no Accepted ADR. | ADR-0064, ADR-0071 |
| [ADR-0060](ADR-0060-asymmetric-agent-plane-keys.md) | Asymmetric per-workload keys on the agent-facing planes — removing the cluster key from `services/mcp-gateway` | **Accepted** | 2026-06-21 | Builds on / conforms to: ADR-0046 (capability tokens + tiered TTLs; this ADR promotes its named P1 trigger — key separation when the blast radius justifies it), ADR-0057/0058/0059 (the invocation /… | ADR-0062, ADR-0063, ADR-0064, ADR-0065, ADR-0066 |
| [ADR-0061](ADR-0061-governed-runs.md) | Governed Runs — operator-initiated multi-step execution above the gateway (Workstream Engine, layer 1) | **Accepted** | 2026-06-23 | No ADR divergence (G2-clean): it extends ADR-0057's invocation surface with an operator/service-service create path and reuses every downstream contract unchanged; it edits no Accepted ADR. | ADR-0062, ADR-0063, ADR-0064, ADR-0065 |
| [ADR-0062](ADR-0062-agent-initiated-runs.md) | Agent-initiated Runs — operator-issued delegation grants as the agent's bounded standing authority | **Accepted** | 2026-06-30 | No ADR divergence (G2-clean): it extends ADR-0061 (a second initiator for the same Run engine) and extends ADR-0046 (a new server-side authority primitive beside capability tokens — it does not cha… | ADR-0063, ADR-0065, ADR-0066, ADR-0068 |
| [ADR-0063](ADR-0063-chartered-workstreams.md) | Chartered Workstreams — schedule-fired, grant-bounded Runs (the standing objective that creates Runs) | **Accepted** | 2026-07-05 | No ADR divergence (G2-clean): it extends ADR-0061 (a third initiator for the same Run engine) and extends ADR-0062 (two additive grant generalizations: a non-agent grantee subject and a windowed st… | ADR-0064, ADR-0065, ADR-0066 |
| [ADR-0064](ADR-0064-persistent-scoped-memory.md) | Persistent Scoped Memory — governed, forgettable memory over the immutable evidence chain | **Accepted** | 2026-07-12 | Builds on / conforms to: ADR-0045 (transactional outbox + content-free audit ingest), ADR-0046 (the canonical capability-edge set — which already names agent-memory as a reserved edge), ADR-0049 (h… | ADR-0065, ADR-0066, ADR-0067 |
| [ADR-0065](ADR-0065-adaptive-workstreams.md) | Adaptive Workstreams — governed agent memory access and agent-composed plans | **Accepted** | 2026-07-16 | Builds on / conforms to: ADR-0046 (the canonical capability-edge set — which has named agent-memory as a reserved edge since v3.0.1; this ADR enforces it), ADR-0056 (the inbound-only MCP trust boun… | ADR-0066, ADR-0067 |
| [ADR-0066](ADR-0066-context-index.md) | Layer 1 Live/Context Index — governed deterministic context retrieval for plan-time composition | **Accepted** | 2026-07-23 | Builds on / conforms to: ADR-0046 (the canonical capability-edge set — which has named agent-workspace as a reserved edge since v3.0.1; this ADR enforces it on one read-only path), ADR-0056 (the in… | ADR-0017, ADR-0067 |
| [ADR-0067](ADR-0067-audit-read-semantics.md) | Audit read semantics — chain-contiguity, server-side filtering, and the lifecycle-noise policy | **Accepted** | 2026-07-26 | Builds on / conforms to: ADR-0045 (the Postgres outbox + the chained-hash extended in-process at the chain authority — this ADR adds no write path and does not touch the append side), ADR-0049 (has… | ADR-0068 |
| [ADR-0068](ADR-0068-paired-operator-sessions.md) | Paired operator sessions — revocable sessions, QR pairing, deployment profiles, and the consent surface | **Accepted** | 2026-08-02 | Builds on / conforms to: ADR-0005 (VPN-first / DMZ-first topologies — §D1 implements its unmet installer clause and changes none of its decision), ADR-0046 (in-process PASETO capability tokens, the… | — |
| [ADR-0069](ADR-0069-audit-chain-authenticity.md) | Audit chain authenticity — signed chain heads, out-of-Postgres anchoring, and third-party offline verification | **Accepted** | 2026-08-10 | — | — |
| [ADR-0070](ADR-0070-release-provenance.md) | Release provenance — signed offline artifacts, SBOM, and an honestly-scoped reproducibility posture | **Accepted** | 2026-08-16 | This ADR governs artifacts and their distribution; ADR-0051 (offline installer) and ADR-0052 (per-OS postgres companions) are extended by one new file each and are otherwise untouched. | — |
| [ADR-0071](ADR-0071-governed-file-transfer.md) | Governed file transfer — widening the egress tool surface without widening the trust boundary | **Accepted** | 2026-08-19 | No divergence from any Accepted ADR — G2 is clean. This ADR extends ADR-0057's typed tool catalog with a second egress family, reuses ADR-0058's broker egress plane and ADR-0053's connector lease u… | — |
| [ADR-0072](ADR-0072-go-toolchain-support-window.md) | Go toolchain support-window policy — superseding ADR-0041's version clause | **Accepted** | 2026-08-24 | Supersedes ONE clause of ADR-0041 — the Go version sentence only. ADR-0041 itself is not edited (G2: an Accepted ADR is conformed to or superseded by a new ADR, never rewritten in place). | — |
| [ADR-0073](ADR-0073-product-boundary-ledger-membership.md) | The product boundary — the ledger defines membership | **Accepted** | 2026-08-27 | It supplies the membership test that ADR-0002 (Accepted, never superseded) assumed but never stated. | — |
| [ADR-0074](ADR-0074-governed-decision-policy.md) | `policy-service` evaluates operator-authored rules, so the decision point can decide | **Accepted** | 2026-08-28 | Decider: CEO. Discharges ADR-0054 §1 and §3 — both Accepted 2026-06-01, both only half-built. This ADR supersedes nothing and edits nothing — it builds what ADR-0054 already decided, and adds the o… | — |
| [ADR-0075](ADR-0075-open-source-licence.md) | ADR-0075 — Open-source licence for the product | **Accepted** | 2026-09-01 | — | ADR-0076 |
| [ADR-0076](ADR-0076-contribution-provenance-dco.md) | ADR-0076 — Contribution provenance: DCO, not a CLA | **Accepted** | 2026-09-01 | Discharges ADR-0075's deferred item ("CLA vs DCO", which that ADR left open with an explicit trigger). Refines ADR-0075. ⚠ ADR-0075 is Accepted and immutable under gate G2, which is why this is a s… | — |
| [ADR-0077](ADR-0077-self-contained-postgres-companion.md) | ADR-0077 — Self-contained PostgreSQL companion | **Accepted** | 2026-10-26 | Decider: CEO. Refines ADR-0052 decisions 2 and 3 — how the postgres companion is packaged (decision 2) and, added before acceptance at the CEO's request, which OSes it is built for (decision 3: Ubu… | — |
| [ADR-0078](ADR-0078-first-run-setup-code.md) | The first operator is created in the browser, gated by a one-time setup code that only root on the server can print | **Accepted** | 2026-10-26 | Decider: CEO. Refines ADR-0046 (operator authentication at P0) — where the single bootstrap operator's credentials are stored and how they are first set. | — |
| [ADR-0079](ADR-0079-connector-generated-target-credentials.md) | Target SSH keys are generated inside connector-service; the host's SSH trust is touched only by the operator-run installer | **Accepted** | 2026-10-26 | Decider: CEO. Extends ADR-0053 (connector-service is the sole custody and decrypt authority for target credentials) with a generation path and a bind path — custody stays single. | — |
