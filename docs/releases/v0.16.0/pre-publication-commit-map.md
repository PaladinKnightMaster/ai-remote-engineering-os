# Pre-publication commit-ID map (2026-10-25)

Before this repository was first published, every commit's and tag's author/committer/tagger email
was rewritten from a personal address to the GitHub `noreply` address. **No file content changed** —
every ref's tree hash is identical before and after — but every commit ID changed, because a commit
ID covers its author line.

Documents written before the rewrite cite the **old** IDs (for example v0.15.0's tag `53782dd`).
Find the old ID in the left column to get the commit as published. Tags kept their names; the v0.16.0
release artifacts were rebuilt from the rewritten tag commit and re-signed (see
[`verification_runbook.md`](verification_runbook.md), "Pre-publication history rewrite").

## Release tags

| Tag | Old commit | Published commit |
|---|---|---|
| `v0.1.0` | `437502d` | `ec4cd48` |
| `v0.2.0` | `b35b0b8` | `5fc6a8b` |
| `v0.3.0` | `ccbff9f` | `d6f4238` |
| `v0.4.0` | `874db08` | `9b28550` |
| `v0.5.0` | `981cb0e` | `b8b2609` |
| `v0.6.0` | `8ba5bfe` | `0587d7c` |
| `v0.7.0` | `47622d7` | `5ca90f5` |
| `v0.8.0` | `58c68fd` | `8e098b0` |
| `v0.9.0` | `053997d` | `7ff3a2c` |
| `v0.10.0` | `aab261a` | `a2e9770` |
| `v0.11.0` | `08748b0` | `a0fbdbe` |
| `v0.12.0` | `7214f75` | `4f3c641` |
| `v0.13.0` | `c04554f` | `a27afa7` |
| `v0.14.0` | `6b72739` | `a06d71c` |
| `v0.15.0` | `53782dd` | `a37da1e` |
| `v0.16.0` | `7ca592f` | `e43cece` |

## Every commit on `main`, oldest first (old → published, subject)

```
928ec51b03ea -> dfde05c7b1c1  bootstrap: v3.0.1 skeleton + customized agency overlays
2d30fa68da94 -> ee4ff6d27f8e  docs: point CANONICAL at workspace VERSION_CONTROL.md
13ec3f8d73b9 -> 24d816876506  adr: ADR-0041 Go as primary backend; TS frontend-only; Python sanctioned sidecar
f01719fb2f8b -> cec02f7d6fea  adr: ADR-0042 Monorepo tooling — Go workspaces + pnpm workspaces + Taskfile
4f06785b2ad0 -> 7431bed343a0  adr: ADR-0043 API framework — go-chi/chi v5 on net/http, oapi-codegen chi-server stubs
6b79279eb096 -> 4658279e467d  adr: ADR-0044 DB migration tool — golang-migrate v4 library mode, embedded per service, pure-SQL
8d53234b1b6b -> 1cb3df75e4bd  adr: ADR-0045 event bus — no broker at P0; Postgres transactional outbox + sync HTTP ingest into audit-service; NATS JetStream held as P1 reserve
7249930eeacb -> 64731eaa6163  adr: ADR-0046 auth adapter — no IdP daemon at P0; packages/go/auth/ PASETO v4 capability tokens; Zitadel held as P1 reserve
2afe41973532 -> a0eb38842157  adr: ADR-0047 frontend framework — no meta-framework at P0; plain Vite 6 + React 18 SPA built to static assets; Remix (SPA mode) / TanStack Start held as P1 reserve
b60a1f923eb7 -> 806ebe8ebe8b  tickets: T-011 + T-012 — P1 lint suite closing ADR-0044/0045/0046/0047 verification gaps
97ebb6952438 -> de73273e466b  skeleton: T-002 repo skeleton commit — go.work + pnpm-workspace + .npmrc + Taskfile + Postgres-only MVP compose
a63c4eb3fbc2 -> bc60b3cc6dd0  tickets: T-013 issue-template ladder drift — delete stale inline first-10 list, point to docs/tickets/README.md
dd19974cbc34 -> 3aed6a1ed547  schemas: T-003 shared domain schemas (Organization, User, Resource, Session, AuditEvent)
2714f2b629bb -> 557df2b22a1b  tickets: T-003 — point the Session/RemoteSession naming decision at the codegen ticket (T-009)
6ffae7db79d8 -> d02351b04fda  api: T-009 OpenAPI contract scaffold (shared components + security + envelopes)
58d19b889af0 -> d973e0362a04  audit-service: T-004a shared Go scaffolding (middleware + migrate + codegen wiring)
6adb511f5e65 -> 1476810b6e5a  audit-service: T-004b minimal append-only API (chain authority)
ba748d3f7f87 -> f919f23cb944  audit-service: T-004 toolchain verification — build/test green; fix two bugs
90b6f8907259 -> 3c546779f7b5  chore: add .gitattributes to normalize line endings (LF in repo)
fd936142c258 -> daedb83e5892  tickets: T-014 — per-service OpenAPI contract slicing for oapi-codegen
12162478b1c5 -> 223ae383f8fb  audit-service: T-014 per-service oapi-codegen slice + re-seat onto ServerInterface
98de1eec13e9 -> f01891cfe246  ci: T-010 — GitHub Actions for build/test/validation + schema validator
15195996ab6f -> 76931725516f  auth: T-008 packages/go/auth PASETO v4 capability tokens + audit-service wiring
2c62b49e89ca -> 86ab52045b67  api: T-005a services/api skeleton + resource-registry store + migration
bb166d511756 -> b63d7962eeac  api: T-005b resource registry HTTP surface (/v1/resources CRUD)
763516f81493 -> 9a5ae3b2358f  auth: T-015a operator auth primitives + provisioning (argon2id + authctl)
31abbc0d8dfa -> 35870ca5b6e3  api: T-015b operator login HTTP surface (/v1/auth/login + /v1/auth/me)
b0c2bd18ca70 -> eacd066c9a94  session-broker: T-006 skeleton (HTTP/2 + WebSocket transport)
6f2a77f78a3c -> 0a6d002b1f6e  frontend: T-007a JS toolchain + packages skeleton + TS codegen wiring
4a3965cbddba -> ab890a068566  frontend: T-007a tightening — vitest on the CSRF/401 contract, tighter dep ranges
c9e3ee9cb1e7 -> b23d0d1f7d03  docs: T-007a ticket — align with what shipped after tightening
b9c170dcec81 -> 05acf98492a8  api: T-007b login cookie reconciliation (HttpOnly session + double-submit CSRF + sliding refresh)
a5084ae83b61 -> d34bde4627c7  api: T-007b tightening — crypto/subtle, OperatorTTL-derived test, Login CSRF noted
ef025b2412fd -> 166d16d83c5a  web: T-007c apps/web shell + capability UI + login consumer
3caa54c5c0e5 -> 69457bbf3e08  web: T-007c tightening — align config var refs + hook docstrings
62752e8017d6 -> c4c293dd32fd  docs: v0.1.0 release criteria (the exit gate for every P0 ticket)
f2d82b9e3f04 -> a64ae213875a  docs: v0.1.0 release criteria — Accepted after war-room consult
72f878a7e9d3 -> 321ab0455873  lint: T-012 JS-side lint suite — SEC-7 release-blocker
d31c1545a2a7 -> ac1cd7a1fd27  lint: T-012 tightening — close member-expression bypass + eval surface
94323810a3c6 -> 9979fd7e5c46  lint: T-011 Go-side lint suite — SEC-8 release-blocker
8bb00375561d -> 3672b0d4cb1c  lint: T-011 tightening — close bypass surface in migrate-lint + forbidden-features
a7e7cccf7a58 -> a78ffc426958  docs: ADR-0048 (Proposed) — cockpit session lifecycle + criteria amendments
c60a43686f6a -> 3eac247a9c4d  docs: ADR-0048 Proposed → Accepted (post-spike) + findings post-mortem
b1264d614b1d -> f9262207a1ab  T-016a: sessions + resource-secret migrations + pgcrypto provisioning
35abe819a719 -> fa8e54087665  T-016b: /v1/sessions REST control plane on services/api
0267914bf072 -> d0fefd87e7f9  T-017a: packages/go/outbox + session-broker persistence spine
bf70131d49d4 -> 9edd7fc379f4  T-017b: session-broker SSH-over-WS data plane + lifecycle audit + cast writer
2b10c0c470cb -> 26bcde647a28  T-018a: shared CookieToAuthHeader (broker WS auth) + TS client regen
6fa8fba384be -> 33c7edc0ec22  T-018b: <TerminalView> (xterm.js) + session list/start/end UI
494276c7fb0f -> a5ae3dcfd6bc  T-019a: terminal.input.line per-input-line audit (ADR-0048 §6 conformance)
13b99c733496 -> d1d09bba214e  T-019b: cast-file retention sweep (casts gc) + audited deletion
1a755b42b846 -> b8f38e8168e1  T-020a: fix session close-coordination audit gap + scope reconnect (v0.1.0)
cb172ce4f839 -> a0729ba08cac  ADR-0049: cockpit audit content model + close authority + reconnect scope
9799de98b45a -> 4f424cce9756  war-room: add mandatory consult gates (e2e-verification + ADR-divergence)
c26e1c7727f0 -> dec9bfc317e7  T-020b: live SSH cockpit e2e harness + fix broker WS auth audience
a79b85fad138 -> 5b718ccf697d  docs: refresh module-map + CANONICAL for outbox, stateful broker, ADR-0049/0050
275cb9b085eb -> 1dfecfaec6c3  ADR-0051: offline-install architecture (Accepted)
2681b8524261 -> 9664bd1ea54a  T-021a: offline-bundle builder (app tier) — §OI-a, ADR-0051
5d027aabe143 -> 8646258dce44  T-021b: install.sh offline installer — §OI-b, ADR-0051
219f617c6707 -> fef8d7eda799  T-021c: offline postgres companion artifacts + guide + DOC-5 — §OI-c, ADR-0052
12256cd30827 -> c39431376f6c  T-022: services/api outbox + auth audit events (SEC-9)
2fddb7564717 -> a2ce38daa44f  T-021d-a: WD-1 systemd bring-up harness + audit-service verify CLI
4e6397cf2f5d -> 8c042de9495e  T-021d-b: WD-1 wedge demo on the installed stack via Caddy HTTPS — closes OI-3
d953447bdc72 -> dc981d78bb39  T-025: release-check + doc-check aggregators (§11a-1, VER-3 / DOC-1)
d36a99ec1a27 -> c10c49e2af7f  T-026: task licenses license gate + license-policy.md (§11a-2, SC-6)
6ffa2fc45e6a -> 72acd576e3ed  T-027: v0.1.0 release artifacts (§11a-3, DOC-6)
32e0e9699ec1 -> 849fba1a7de6  T-024-a: services/api audit read-proxy — GET /v1/audit/events (v0.1.0)
437502d6153e -> ec4cd48e4617  T-024-b: operator audit-timeline SPA + WD-1 read proof (closes T-024, v0.1.0)
af18482e431d -> d91ae7aaa05e  docs(release): record v0.1.0 tag in release artifacts
415565d40dd5 -> b483bfc11f06  docs(v0.2.0): scope "Governed Egress Foundation" — criteria + ADR-0053/0054 (Proposed)
31dba85181ac -> a59a329738df  T-028: ratify ADR-0053 (connector-service) + ADR-0054 (approval/policy) — Accepted
1a26dc0bcc73 -> 54ec0486a5f1  docs(tickets): add v0.2.0 "Governed Egress Foundation" planned ladder (T-029..T-033)
bf6f5f65ac2b -> 6e550e85daee  T-029a: connector-service credential-custody persistence spine (ADR-0053)
93063f2d6aba -> 2b6226fd8d8c  T-029b-i: connector-service outbox + serve spine (ADR-0045)
6ce65d023aed -> 55f2532372ee  T-029b-ii: connector-service credential-lease RPC + atomic egress audit (ADR-0053)
f1911cc2c913 -> 4f5181e89d0d  T-030a: broker re-point onto connector lease + api decrypt removal (SEC-13 code half)
a1c81a9ab013 -> b51352cb1718  T-030a fixup: distinguish broker lease failure modes + restore non-empty-key guard
95ac51567aa3 -> a98291f04023  chore(wd1): fail-loud guard — WD-1 non-runnable until T-033 re-points provisioning
f2960095c05c -> 71d929902f39  T-030b: live broker→connector→audit credential-lease proof (SEC-13 runtime / G1)
9a9be4d00eb3 -> e625aa4cbda7  T-031a-i: policy-service spine (schema + outbox + serve/healthz)
6a3c0a233d9f -> ff794f37c181  T-031a-ii: policy decide/resolve RPCs + audited store + fail-closed sweeper
1b7c161a44eb -> 73c05c4e524c  T-031b-i: api consults policy at session-create + pending_approval status
7e6cdc6638e9 -> 3e45b0d3fa65  T-031b-ii: operator approve/deny resolution (POST /v1/sessions/{id}/resolve)
1d61a88fe449 -> 0e6bcb45c13f  T-031c: <ApprovalPrompt> + apps/web pending-session approval round-trip
2fab0888704d -> 612f69dd6261  T-032: api-owned stuck-session reaper (close stranded pending / pending_approval)
3a121697a463 -> a146c47841f3  T-033a: WD-2 live approval e2e — policy decide/resolve + approval round-trip on real wires
5f6acdb005da -> f7570a70c948  T-033b: OI-6 offline re-point — connector+policy shipped, key relocated to connector, WD-2 green on the installed stack
d309891daf57 -> 3f2fb1235ea2  T-033c: v0.2.0 release closure — VER-5 legs, SEC-13a gate, ADR-0055, DOC-8/9, release artifacts
8d38ed96ebaf -> 5efc5d805765  T-033c-fix: pre-release review remediation (2 findings)
5497261ff39b -> 52f130fdbf72  T-033c-fix2: import xterm.css in <TerminalView> (latent v0.1.0 rendering defect)
b35b0b8414b3 -> 5fc6a8b3595b  docs: record the s12 walkthrough UX findings as v0.3.0 deferrals
a81a3bdc075c -> 21e5a8f8b9fd  T-034: browser smoke suite (headless §12 walkthrough) + browser-evidence DoD addendum
5dcd996bdaac -> 3890c6996ef7  docs(v0.3.0): Accept Typed MCP Gateway release criteria (scoping consult)
46838708dec7 -> 6a4cb4ff73df  docs(adr): ratify ADR-0056 + ADR-0057 (Typed MCP Gateway architecture)
313b79d471de -> bf6ab9bdc829  T-035: services/mcp-gateway skeleton (the first agent surface)
61ef6edfd7ab -> 84ab5e0a2ef7  T-036: agent identity — IssueAgent + operator-gated POST /v1/agent-tokens
834c86f5f55a -> 3c1a2b3a052e  T-037: typed tool catalog + remote.exec definition + tools/list (ADR-0057)
82fad6e2c286 -> 386907821848  docs(adr): ratify ADR-0058 (broker non-interactive exec egress + audit model)
1f132a958733 -> 4c8bab400531  T-038a: broker non-interactive exec core + /v1/exec RPC (ADR-0058)
8a03e6ca2c44 -> c1a98c2b99c5  T-038b: live e2e proof of broker exec egress (G1 capstone for the egress leg)
8a38b02008a3 -> 175386df94e0  T-039a: gateway stateful spine — invocations table + store + outbox (ADR-0057)
04ac117a3217 -> dce78c992ab3  T-039b: gateway governed orchestrator — the catalog Invoke drives the spine (ADR-0057)
7790cbc124ed -> 44f4644771f7  T-039c: live allow-path e2e — the gateway drives the spine (G1 capstone for T-039)
0e0d5dde327e -> 7bbd13444873  ADR-0059: informed invocation approval — command visibility + parked-invocation lifecycle (ratify, v0.3.0/T-040)
c5d3196858e4 -> 00087fe4d748  T-040a1: gateway block-and-wait + command persistence + invocation reaper (ADR-0057/0059)
ab8aa0b0e520 -> 453e8ef152dc  T-040a2: gateway operator resolve/list plane — block-and-wait unblock end to end (ADR-0057 §4 / ADR-0059)
1930c9cace11 -> 7e9e0448ed77  T-040b: api operator proxy for invocation approval (AP-2; ADR-0057 §4 / ADR-0059 / ADR-0050)
f54ff95b0f6e -> 9c2440120ec7  T-040c: operator pending-invocations UI + browser smoke (FE-11; ADR-0057 §4 / ADR-0059)
a58c27c73454 -> d10fabd2eee0  T-041: WD-3 live e2e capstone — agent surface → block-and-wait approval → governed exec (G1; ADR-0056/0057/0058/0059)
19101fb3a88b -> 90e2b5bcf742  T-041h: v0.3.0 audit hardening — enforce ops-scope, cap request bodies, pin internal plane to api (SEC-16/SEC-19; F4/F5/R4/R5)
f9ac820c5798 -> b90c975e7a45  T-042: cockpit-polish rider — resource picker + audit-table truncation (FE-12)
2fc7c249c5fd -> 860024f1ad85  T-043a: OI-10 — mcp-gateway into the offline + installed stack + installed WD-3 (ADR-0056/0057/0051)
4c337609d515 -> e783cab1c585  T-043b: v0.3.0 release closure — artifacts + F6 doc clarification (release-candidate)
60e5336af5e1 -> f128e41311a3  T-044: fix operator pending-list re-prompt on approve (ListPending excludes granted) — found in human review
2318e23dd2c5 -> d39f61cbde87  dev: local review tooling (compose port overlay + WD-3 PS driver + Docker reclaim)
ccbff9fb28b9 -> d6f4238e767f  dev: docker-reclaim - gate the VHDX compact on Administrator elevation
2966972a69de -> 9a2f42227dd6  docs: v0.4.0 release criteria ACCEPTED — Hardened Agent Foundation
0cf9fd83e97f -> d69b242f05a2  docs(adr): ADR-0060 ASYMMETRIC agent-plane keys — Accepted (v0.4.0)
d93747be37f7 -> 23f179ef51b0  T-045: PASETO v4 public (Ed25519) primitives in packages/go/auth (KEY-1)
b86cfbecf042 -> 37820fdfd411  T-046: dual-mode verifier in packages/go/auth (KEY-2)
5e8ec08974e0 -> 435ce9ccaa75  T-047: api signs agent-tool tokens asymmetrically; gateway dual-mode verify (KEY-3, agent plane)
573827f8a10d -> 6e9f3c809904  T-048: peers accept gateway-asym (policy/audit/broker dual-mode) + api->gateway service token asym (KEY-3)
b2d6b56f9aa5 -> 9e9e11305f79  T-049: the gateway goes keyless — outbound asym + drop AIREOS_PASETO_KEY (KEY-3, SEC-20)
82d1a780e6c2 -> 7819a9ad1682  T-050: R5-ext subject-pins on the broker-exec and connector-lease planes (PIN-1/PIN-2)
ed8f64078f2e -> 8793bb7f3252  T-051: F3 — command at-rest encryption (gateway-local AES-256-GCM)
d9356ae7b3b0 -> 7ee1aa19f8a6  T-052: WD-4 capstone — SEC-21 blast-radius negative test (keyless gateway can't forge a foreign edge)
89c1fd3ab461 -> 0d7fc9525478  T-053a: OI-11 — provision the asymmetric/F3 keys + key the installed gateway keyless (SEC-20)
85aa640efdbf -> f5359b8092ca  T-053b: DOC-12 + v0.4.0 release artifacts + bundle version bump (release closure docs)
a93e7daea880 -> 80d4577468a9  docs: v0.4.0 CI/CEO verification runbook
874db087aac5 -> 9b2855019dea  docs(wd1): commit the §human WD-4 demo toolkit (reproducible installed-stack review)
79e164b9f950 -> 9a561c7ba490  docs(workstream): v0.5.0 workstream-engine tech spec (operator-initiated wedge)
ad24e53dc1ca -> ad8c2d9eb0bc  docs(adr): ADR-0061 Governed Runs (Accepted) + tech-spec revision + CANONICAL ladder
12e28970a464 -> 79084be44c2b  docs(criteria): v0.5.0 "Governed Workstreams" (layer 1: Runs) release criteria — ACCEPTED
52bcd54bfa74 -> a399e6828fce  T-054: Run schema + internal/run store skeleton (v0.5.0 Governed Runs)
6a5a3c9acda7 -> 410dae33b46b  docs(canonical): sort ADR ladder tail to numeric order (0057-0061)
89714a860589 -> 0d67d5171945  T-055: extract runGovernedStep — gateway governed-core split refactor (RUN-2)
bed13e2ad095 -> aa6225cb24f2  T-055b: gateway /v1/steps route + RUN-5 operator-identity (Governed Runs)
76e086f6d260 -> 933548940334  T-055c: api Run sequencer + gateway-step client (completes RUN-1)
032f3e0f804f -> fc86222812ac  T-055d: live e2e — api Run sequencer drives a Run through the governed gateway plane
66570c4e60f4 -> 783ddf1d2667  T-056: reclaimable claim-lease + crash-resume + running-orphan fail-closed
059778386cc5 -> 62f9a133f1a6  T-057: operator Run launch route + status + bounds (SEC-23, GOV-1, PERF-6)
2034ed4b3080 -> defeabd2fe08  T-058a: Run audit linking — terminal run.* events + step->invocation correlation
1730aa400938 -> 085bda1c95ea  T-058b: Evidence Packet GET surface + DUR-4 gateway stale-running reaper
2910a4724355 -> a8e63d57867c  T-059a: operator Run cancel + list routes (DUR-5)
204897782bff -> 53e9cc3bf1d0  T-059b: operator /runs UI — compose, informed launch, status, cancel (FE-14/15)
d8f6e083b2f9 -> 43ed970afbb4  T-059c: parked-step Run context on /invocations (FE-16)
085ebc73f3f0 -> 54f46b9bf181  T-060: WD-5 live e2e capstone — happy path + denied-abort (G1)
c50b8b124994 -> 12e614c052ea  T-061: OI-12 — installed WD-5 on the offline stack (G1)
887f152e866e -> 8f75c996c811  T-062: DOC-13 + v0.5.0 release artifacts (release closure, Phase A)
981cb0ed6bce -> b8b2609bfebf  T-062: v0.5.0 release closure — flip artifacts to released
b07e887f8c49 -> 94525ebb8415  docs(adr): ADR-0062 agent-initiated Runs via delegation grants (Accepted)
edd5dfa488a1 -> 8df8884fc37f  docs(release): v0.6.0 release criteria — Agent-initiated Runs (Accepted)
dbe590cfa58f -> fd710b8c3520  docs(release): clarify v0.6.0 criteria — gateway->api verify edge, run.launch scope (post-accept self-audit)
2525a6c01b07 -> 3ab837755c9d  feat(api): T-063 delegation_grants schema/store + issuance routes (v0.6 DEL-1/AUD-16/SEC-26)
f63e519b02ac -> 375dfdc253bd  feat(gateway,api): T-064 actor/principal split — structural plumbing (v0.6 SEC-28/DEL-5)
737d7a012ef4 -> b13c92239d28  feat(api): T-065a delegated-launch inbound — dual-mode verifier + plan-vs-grant (v0.6 SEC-27/29)
714d0df6e68e -> 7838dbf3bf43  fix(api): route-scope the gateway verifier — operator routes stay symmetric (T-065a self-audit)
50e84319360e -> 51cc5e822b75  feat(gateway,api): T-065b run.launch tool + sequencer dispatch — agent-launch loop (v0.6 DEL-2/GOV-4)
74569f9d0605 -> 42d1c8fb5c42  feat(api): T-066a revocation cascade + grant bounds (v0.6 DUR-7/PERF-7)
25dd2ba2cd87 -> dd61dd1efadd  feat(web): T-066b delegated approval context on /invocations (v0.6 FE-18/GOV-5/6)
7d5b07d80250 -> 3315c9fe304f  fix(api): enforce the per-operator active-run ceiling on delegated launches (T-066 self-audit)
8212ef00907f -> de4eb210140c  feat(web): T-067a the /grants operator UI (v0.6 FE-17)
40fc823dc9bd -> 624668150708  feat(web,api): T-067b agent-Run visibility on /runs (v0.6 FE-19)
0bb25be7bf76 -> 303845c4195f  test(e2e): T-068a WD-6 live capstone — agent-driven grant-bounded Run + revoke-cancels-in-flight (v0.6 G1)
bb5529e54156 -> 96b05aed12c1  feat(api,web): T-068b grant id on the parked delegated view + grants-form polish (v0.6 GOV-6)
0ac906830c2f -> 91622c296780  feat(install,wd1): T-069 OI-13 — v0.6 delegated-launch plane on the offline/installed stack
621a8aed47ac -> 01101bca0cc9  docs: T-070 Phase A — DOC-14 + v0.6.0 release artifacts (release closure)
501e672e67d8 -> 0e418b8844b8  test(e2e): T-070 pre-release delegated-plane edge + stress sweep
8ba5bfeb0e90 -> 0587d7c3520d  docs: T-070 v0.6.0 release closure — flip artifacts to released
eb1abbab05c9 -> e1028cf044a2  docs(adr): ADR-0063 Chartered Workstreams — v0.7 opening ADR (Accepted)
c2ea543f911e -> d28207baa8f8  docs(release): v0.7.0 release criteria — Chartered Workstreams (Accepted)
691ee9f3cd97 -> 246ae9d324ed  feat(api): T-071 workstream charter — schema/store + routes + in-tx grant issuance
3961eac0497d -> 737b17d45495  refactor(api): T-072 shared bounded-launch core — one validator, two callers (SEC-30a)
9fa7aa033d59 -> 79fd5c95ccc2  feat(api): T-073 workstream ticker + windowed budget + self-retire (WS-3/WS-4)
4944fad39bf3 -> c15f95eb11c4  feat(api): T-074 workstream lifecycle verbs — pause/resume/retire + the retire-cascade (WS-5)
87b2c24bdb4e -> 1bf2cae8f0fe  feat(web): T-075 /workstreams operator UI + scheduled-actor legibility (FE-20/21)
31c199b0e467 -> 07ac76401522  test(e2e): T-076a WD-7 capstone — schedule fires live (recurrence + flagged park/approve)
7dfb538c9329 -> 9c8b7ae6fcd0  test(e2e): T-076b WD-7 negatives — retire-kills / pause-resume / budget-window / self-retire
1686493bd38c -> d4be66bd34eb  test(wd1): T-077 OI-14 — installed WD-7 on the privileged-systemd stack
c345c6c558f3 -> 0c90daf09931  docs: T-078 Phase A — DOC-15 + v0.7.0 release artifacts (release-candidate)
d88139e67eea -> bd141ff8831a  fix(web): drop the hardcoded 60s client-side floor hint on the charter interval
47622d722681 -> 5ca90f59e160  docs: T-078 Phase B — v0.7.0 release closure (CEO §human WD-7 signed off)
1f34d8629ec8 -> 4646bd53e11a  docs: ADR-0064 Persistent Scoped Memory accepted — the v0.8 theme (P2 tier opener)
67bddd332029 -> 09fde88fab81  docs: v0.8.0 release criteria accepted — Persistent Scoped Memory (WD-8 gate)
685145378916 -> cd4debfe9b2b  feat(api): T-079 memories schema/store + accrual on the shared terminal flip (MEM-1/MEM-2)
096ebab1fa50 -> fd3f20c2a29f  feat(api): T-080 /v1/memories routes + codegen + owner-scope + sealed authored writes (MEM-4 partial/SEC-31/PERF-9/VER-24)
8807fc4bde04 -> 493d23ed1368  feat(api): T-081 memory forgetting verbs + expiry sweep reaper-fold (MEM-3/DUR-9/AUD-19/F-b)
748a1a9ba2ca -> baab850ab666  feat(web): T-082 memory UI — operator surface, charter-detail promotion, export
dca2b2219d93 -> dea9338144c3  test(e2e): T-083 WD-8 live capstone — accrue/read/redact/expire/pin + N1-N4 (G1)
23c7f89b5a39 -> 5e491baab4af  test(wd1): T-084 OI-15 — installed WD-8 on the privileged-systemd stack
a1a5bc3bc45c -> 4e88041df83b  test(wd1): trust Caddy's local CA in the harness — drop InsecureSkipVerify
95eec94cee4f -> 77042c04362f  docs: T-085 — DOC-16 + v0.8.0 release artifacts (release-candidate)
58c68fd7d919 -> 8e098b0de71f  docs: T-086 Phase B — v0.8.0 release closure (CEO §human WD-8 signed off)
80de0dc38d66 -> ec8157e76b7b  docs: ADR-0065 — Adaptive Workstreams (governed agent memory access + agent-composed plans; Accepted, v0.9 binding spec)
116cf2dee2b4 -> 7cc4061cbaf9  docs: v0.9.0 release criteria — Adaptive Workstreams (Accepted; C1-C5 + F-a/F-b/F-c baked in)
d60d4fe405a9 -> b85f603c13c5  fix(e2e): guard nil TLS transport in fireHeldInvoke (typed-nil RoundTripper panic on plain-http task e2e)
3ea01db136ad -> 6307e946cea6  feat(api): T-087 — migration 0013 + adaptive/plan-request/memory-visibility schema + stores
c22f8d1d75e9 -> 79caa8fd845d  feat(api): T-088 — LaunchBounded disposition split + proposed Run + approve/deny (RUN-6, C1, C3, F-c)
fabe2c199642 -> f7ea5ae7533c  feat(api): T-089 — ticker adaptive branch + plan-request lifecycle + cascade re-aim (PLAN-1, DUR-10, C2, D3)
88118636ca99 -> 72b2cd210516  feat(gateway,api): T-090 — workstream.poll/plan.submit/memory.read + the agent-memory read plane (MEM-5, SEC-32, AUD-20, F-b)
c16037148d0e -> 66a9ba4a3559  feat(api,web): T-091 — memory share flip + adaptive charter form + informed plan preview (MEM-6, FE-24/25, GOV-10)
e20918b4778c -> 41dd47ffe647  test(e2e): T-092 — WD-9 closed-loop capstone (happy path + N1-N5; G1)
a695f4db355d -> 6ee6bd282a10  test(wd1): T-093 — installed WD-9 at the production posture (OI-16, C5)
32aa6358dec1 -> cce58388143a  docs: T-094 — DOC-17 + v0.9.0 release artifacts (release-candidate)
053997dced84 -> 7ff3a2cc7aeb  docs: T-095 — v0.9.0 release closure (CEO §human WD-9 driven live + signed off)
4da047c61826 -> 019b4f9ce79e  docs(references): v4 reference intake + first-pass triage
fc851744c3a3 -> 026c2b2b9473  docs(v0.10): accept ADR-0066 + v0.10.0 criteria (Layer 1 Live/Context Index)
09016ded2a5f -> 9cb08afc403a  feat(context): T-096 — migration 0014 + internal/context store skeleton + confined-root reader
9eb255c49da2 -> e8c27c5804c8  feat(context): T-097 — rename internal/context to ctxindex + the incremental indexer
fd26cc1efbb2 -> b877a4fadfc9  feat(context): T-098 — SearchForAgent grant-scoped FTS read + context.read chaining
0c1be7571975 -> bcd30570ec67  feat(context): T-099 — context.search gateway tool + forward + subject-pinned api route
a7744fe540c7 -> f4b08c76fb42  feat(context): T-100a — operator /v1/context-sources* routes + in-tx audit events + codegen
ef765e0d60cd -> 84cb0be3c0d3  feat(context): T-100b — operator context-source UI on the workstream charter detail
80c65b4ec4cf -> b02b874de3f3  test(e2e): T-101 — WD-10 live e2e capstone (Layer 1 Live/Context Index)
bbf279436197 -> 9d110e4bf008  test(wd1): T-102 — OI-17: WD-10 Layer 1 Live/Context Index on the installed stack
59f35469b362 -> dd0a9f0c57ec  docs(v0.10): T-103 — DOC-18 + docs/releases/v0.10.0/ artifacts (release-candidate)
aab261accd46 -> a2e9770fe4aa  docs(v0.10): T-104 — v0.10.0 release closure (CEO §human WD-10 signed off)
03e83831f9d4 -> e396f82b6474  docs(refs): Step 1a — v3/v4 reference audit, tiered decision layer, activated watchlist
7e69c1cf4ce5 -> 351fc4981f9f  docs(reqs): Step 1b — requirements/backlog v4 + epic-story catalog
05ec99cb9d37 -> 9e785876d9f3  docs(reqs): ratify the Beta-1 path (v0.11 -> v0.12 -> v1.0)
f6e43e81a7d0 -> f99c127ef07a  docs(adr): accept ADR-0067 audit read semantics + v0.11.0 release criteria
6b05bcdb28b5 -> d22cb85fb0ab  docs(criteria): amend v0.11.0 criteria from the T-105 consult grounding
56485536039e -> 7590c16f95d1  feat(audit): reverse-cursor keyset List, governed-actions@1 preset, chainHead (T-105)
c8a3c767b83c -> d14950cb76a6  fix(audit): make ListQuery.build() filter clauses index-reachable under a generic plan (T-105 follow-up)
e4a970b691ea -> 5bced1d21aa9  feat(audit): light up the audit read contract for both consumers (T-106)
02b37beed8e1 -> a7c31ac127d7  feat(web): audit view opens newest-first on the governed-actions preset (T-107)
2e43eba69e0b -> 58eeafaf740e  fix(web): commit audit text filters on blur/Enter, discard stale responses (T-107 follow-up)
6a625ab6de2f -> af9161c3ce38  feat(web): live detail refresh for the charter panel, targeted and identity-guarded (T-108)
c9ad30bdd406 -> 629e34d930ff  fix(api): resolve charter-scope memory accrual at read time (T-109)
034ba3e030df -> ccf0249d66c4  fix(e2e): scope WD-10 leg 4's context.read count to a per-call baseline (T-110)
4e6c0e259f1a -> 9cebc34f9b81  docs(releases): v0.11.0 Phase A closure artifacts + requirements ladder (T-110)
0ec5059bc7e4 -> c5d920d433e3  fix(web): honest empty state on a filtered audit view (T-111)
9dc3e99ad1ad -> 3a8dea2be802  docs(releases): GATE-3 script + §human findings for v0.11.0 (T-110 Phase B/1)
bcb4a8ef8261 -> 522f09a449cc  docs(releases): v0.11.0 RELEASED — GATE-3 signed off (T-110 Phase B)
08748b050f9a -> a0fbdbedd2eb  docs(releases): runbook header reflects the released state (T-110 Phase B)
81ad2956aff6 -> 791b117a799d  docs(adr): ADR-0068 paired operator sessions + v0.12.0 release criteria
d7fafb0cbdd6 -> f414ccb7fce4  chore: bump kit reference to v4.6.0
e306c4a0b951 -> 5d478aeeea86  feat(install): required deployment profile + enforced vpn/dmz site-address assertion (T-112a)
3080d33bfb9c -> 7756aca1f73c  fix(install): make OI-23's re-run precedence provable (T-112a addendum)
427ebfe94634 -> 0d997c71f3b3  feat(api,web): surface the active deployment profile on /v1/auth/me and in the cockpit (T-112b)
67c996026c54 -> 78b0cd205454  feat(api): pairing requests + revocable paired sessions data layer (T-113)
5b407b006f1e -> 2e6ef6fb04db  fix(api): close the malformed-id oracle + single-clock the redeem expiry check (T-113 review)
87210b5b0196 -> be9b3e0baa96  fix(api): compute pairing expiry in SQL, not Go, so the clock-authority comment is true (T-113 review)
389c7f44913c -> 1ec0f1c37def  feat(api,auth): paired-session token tier + pairing contract, routes disabled by default (T-114a)
c64e4e88c7fd -> 89de32777806  fix(install): AIREOS_PAIRING_ENABLED survives a re-run (env > persisted > 0)
bbebbfce1fbf -> 1dff28f26761  feat(api): paired-session plane — liveness, approvals@1 enforcement, pairing audit (T-114b)
a67b229c56d1 -> 512bae42de0e  docs(api): disclose AUD-34's audit-actor fork + assert it directly (T-114b review)
118c0274dd17 -> 1c35272a1d07  feat(web,ui): vendored QR encoder + desk pairing surface (T-115a)
0f81f6c37e60 -> 55f879522f0a  test(web): jsdom coverage for Pairing's SEC-41 degrade, error path, one-shot secret (T-115a review)
a90575904108 -> dd277756249f  feat(web): /pair redeem + scope-aware shell + mobile approval surface (T-115b)
c7a663953409 -> ac288b15d1ca  feat(ui): Button with variants, sizes and real interaction states (T-116)
e0ac631f090a -> 16c29fa044e1  fix(config): make the SEC-7 fetch exception actually apply
93bd3a7499ae -> a14fc1ecdd88  refactor(web,ui): migrate every presenting button to the Button component (T-117)
c0b2127cfed0 -> 537c2ebe471c  feat(web): first-run checklist derived from live state (T-118a)
4bf8cef9d9b9 -> 5e2af2a8733a  feat(web,offline): loopback demo target + first governed action (T-118b)
170cd01c944f -> 33a2230ecc2b  fix(broker): constrain host-key algorithm negotiation to the pinned key (T-123)
1a0ebca3092d -> 45601c574770  feat(api,web): governed resource deletion with a referential guard (T-118c)
de799426908f -> b6e3e9d170fd  fix(web): single-source the demo resource name (T-118c review)
bac1267fa093 -> d0657c02cecf  docs: first-run quickstart to a governed action; record ONB-6 drop (T-119)
9049f44fc8f6 -> 693f8be05a87  fix(offline): ship setup-loopback-demo.sh in the bundle; clear the stale host-key warning (T-124)
dde1c6100652 -> bbb57b4f6c69  docs(releases): record the E-05b drop; correct FE-42's rationale (T-120)
2707a021a0fb -> a43a624b42be  test(e2e): WD-11 pairing walkthrough + negative leg on compose (T-121)
af6fd80e2585 -> fc23f2fee5ac  docs(releases): correct AUD-34's event names; record the audit-actor fork
a8155b6be1da -> 49ec3426133e  test(release): installed-stack gates for v0.12.0 — WD-11, regression, upgrade leg (T-122a)
fd8c767c7582 -> 34688b6b9e1c  test(wd1): widen the captured go-test window so WD results are visible
2964aa487f6f -> 981f6b72e917  docs(releases): v0.12.0 closure artifacts + requirements reconciliation (T-122b)
1865155c6466 -> d4765951e0c4  docs(releases): runbook — how the second device actually reaches the stack
322f1fab20e9 -> 40fdedd24c72  fix(wd1): bringup-demo must not hard-code a bundle version
814a2f1acdc7 -> 4c33f92ed838  fix(offline): sshd precondition must test reachability, not unit state
7214f755a7d9 -> 4f3c64153347  docs(releases): v0.12.0 RELEASED — GATE-4 signed off (T-122c)
01a376aef6c5 -> 217f056545db  docs(requirements): post-Beta roadmap war room — re-ordered path + boundary rule
239c0e79cde7 -> c35403d2aa50  docs(releases): v0.13.0 Evidence Hardening criteria — E-06 splits, freeze defers
ae6b8b7be3b7 -> e2ea6d46e5c2  docs(requirements): pgGraph RLS verdict — CONFIRMED, and the disqualifier is caller-asserted scope
ad1123fef2d0 -> 414faa63a918  docs: reconcile CLAUDE.md phase, release target, and bootstrap rules
198a4f571891 -> 634538be5d86  docs: reconcile the planning record before the v0.13.0 ladder starts
38b40d636ca4 -> f2d419114550  docs(adr): ADR-0069 — audit chain authenticity (T-123)
93f3e39624b7 -> ea6fa5dce23d  feat(audit): audit_chain_heads schema + Ed25519 chain-head signing (T-124a)
a2ec29f9c1c6 -> b5db45a25f86  fix(audit): CheckpointTx connection-isolation + signed-head golden vector (T-124a follow-up)
60f207c85143 -> 056983c7576d  feat(audit): chain-head cadence sweep + graceful shutdown + checkpoint subcommand (T-124b)
e5640a7f2a6e -> 63f6d8e2de19  feat(audit): installer mints the chain-head signing key; absent key is now fatal (T-125a)
39177784958f -> 6272212abca5  fix(audit): derive the audit chain-head public key on partial state instead of regenerating (T-125a follow-up)
477793ecc4de -> 51053076b0d2  feat(audit): anchor signed chain heads outside Postgres + startup re-sync (T-125b)
a11cc2ae126d -> 1d3ea7eadfc6  feat(audit): streamable NDJSON evidence export for offline verification (T-125c)
ba0e55c2fedd -> cfdee5fe9890  fix(audit): bound the evidence export's two passes to a captured snapshot seq (T-125c follow-up)
725c67fd21bb -> dc7718250c15  fix(audit): classify divergence by head count; verify now checks head signatures (T-126a)
27fe990e7fb8 -> 52d6e1c0ac3a  feat(audit): aireos-verify — offline third-party evidence verification (T-126b)
53a2f23987d1 -> addc4ea9ef1d  test(e2e): WD-10 distinguishes a plan-request TTL expiry from a real authorization refusal (T-127a)
e3908e276633 -> b1b2f3918e10  fix(wd1): assert INTACT+ATTESTED on the installed stack, not a bare exit 0 (T-127a)
9b31c1524b7d -> ba0448d3c2ad  test(sec-43): prove a forged audit chain is caught on both stacks (T-127b)
8f8ab9497617 -> a41c47365412  docs(adr): ADR-0070 — release provenance (T-128)
191b001c10bb -> ff95b22a64ba  feat(release): sign both offline artifacts + normalize the archive (T-129a)
cbe2792d6719 -> 569e54236c57  feat(release): CycloneDX SBOM for both shipped artifacts, with coverage asserted (T-129b)
0dca5ce077e4 -> 78c43b160198  test(sec-45): prove a single mutated byte fails release-artifact signature verification (T-130a)
0470cb8c7506 -> 03b433572a46  fix(sec-45): enumerate every signed artifact, honour RELEASE_PUBLIC_KEY, never claim "proved" on a failing run (T-130a review)
27c5bec94e89 -> a28f67243376  feat(release): reproducibility measurement tooling (OI-35, T-130b)
b13dbe8c9b77 -> 12aba6404b50  fix(release): pin Node 22 + pnpm 10.32.1 in the reproducibility build image (T-130b)
e98148a1f412 -> 9a6539574f21  docs(release): publish the OI-35 reproducibility measurement (T-130b)
deebb5b340c7 -> b5dade89291e  docs(release): state the warm-build-cache caveat on the Go layer's reproducibility result (T-130b review)
53d5c6f0b5e7 -> c3daf92dca98  docs(release): cold-cache reproducibility measured — the Go layer's result is no longer a cache tautology (OI-35)
1c9439901869 -> 3495a7dcaadb  feat(api): remove PUT /v1/resources/{id}, chain resource.created (T-131)
a5a3d61b7622 -> e923e40dca75  test(config): enforcement-of-enforcement — VER-12..15 (T-132, S-25/B-038)
3265f00db6a4 -> 55eecbf70bce  test(config): assert every eslint PATTERN matches, not just every block (T-132 review)
1964d88ba798 -> a0daaa19e4f1  chore(record): flip B-038, correct both stale ADR-head claims, bump BUNDLE_VERSION to 0.13.0
7f76e93341b0 -> dc21d89fa09e  fix(release): measure-reproducibility.sh never ran the documented build
7af7420d3f29 -> c8e9fa6aa851  fix(release): S-26 quickstart gate, shell/precondition half (T-133a)
fee5cad30225 -> da64ec420989  fix(release): S-26 quickstart gate, browser half (T-133b, completes OI-36)
61176a7f1e2d -> 3849365794be  test(smoke): assert spec B actually RAN — a skipped Playwright test exits 0 (T-133b review)
41da1b93bd2c -> 6e4de5a64092  docs(security): threat model and disclosure (T-134, DOC-30..35, SEC-46)
7dcacd68a72f -> 54f29437c3ab  docs(release): S-21 security-audit capstone — GATE-1 PASS, GATE-3 re-proven (T-135)
2340aef4e4df -> 90a18e5dc0e6  docs(release): v0.13.0 release artifacts — runbook, notes, manifest (T-136a)
06e43797122b -> 96f2b2b97b38  docs(release): fix runbook Step 4 — the GATE-5 walkthrough did not execute as written
c104a72d5e1e -> 85f719d22ab8  docs(release): runbook 4.7 — make GATE-5's "you needed nothing else" provable (§human finding)
c04554f54d49 -> a27afa74200c  chore: untrack the gate5/ scratch directory and ignore it
e43e46e2c6b3 -> 4ee65a3199eb  docs: reconcile the planning record for the v0.14 re-scope
f0e0732bea4b -> 993a37495008  docs(refs): v6 reference delta — the v0.14 checkpoint scan
952a14e48769 -> 66883ab34dec  docs(v0.14.0): T-137 — ADR-0071 Accepted + release criteria Accepted
97223055fcce -> 57ed771ce4fd  feat(mcp-gateway): T-138 — the governed-step seam (ADR-0071 D2)
7d8ea51f8ee7 -> 0cd5d17f2fa3  fix(mcp-gateway): T-138 review — dispatch on persisted tool, not caller-supplied tool
a7b3bb6ff20a -> e36d029a6317  docs(v0.14.0): correction of record — restore the omitted verification gates, add SEC-52
e501f6a1660e -> fc25c95a907f  feat(session-broker): T-139a — bounded SFTP transport primitive (ADR-0071 D4/D5)
d895876b8ab2 -> dd764b80b3c1  fix(session-broker): T-139a review — atomic write, bounded ListDir
dbe1a6bd474a -> 26bc51237560  docs(v0.14.0): record the four residuals surfaced by T-139a
07b0f8132029 -> f9a5c0b8c9d5  docs(v0.14.0): toolchain currency — VER-23..28 + T-146, CEO-ratified
df4a16a14772 -> 773c56016518  docs(v0.14.0): confirm the lint solution for T-146 — golangci-lint v2.13.0
fe45fed4d618 -> eb80b7684d59  feat(session-broker): T-139b — POST /v1/file, the SFTP HTTP leg (ADR-0071 D4/D5)
de7eb8dd2c81 -> 06d577abbd5d  fix(session-broker): T-139b review — fileBodyLimit's at-cap test was vacuous
7e995348edd0 -> b1b16343c93d  docs(v0.14.0): T-140 audit — the descriptor seam, and an open question for T-141
ee7bec4245b8 -> a1e8cf2f4d2e  feat(mcp-gateway): T-140a — the invocation-descriptor seam (ADR-0071 §D3)
fc0d6756b917 -> 8f24b63025d9  docs(v0.14.0): record the T-140a error-treatment divergence
cfaa98729e2b -> 6d5c05281db5  feat(mcp-gateway): T-140b — remote.file.read end-to-end + D7 SEC-16 narrowing (ADR-0071)
2b261a7bdbab -> 53f767275806  docs(v0.14.0): T-140b review — the descriptor is now a parsed payload
7dcbbc3b6ad5 -> 14e1804f14a3  docs(v0.14.0): §14c DECIDED — option (c), content never enters the database
99449e51489c -> bf0c8a43dc07  feat(mcp-gateway): T-141 — remote.file.write + SEC-49 digest binding (ADR-0071)
3256609d71ef -> 53a5307f48e4  docs(v0.14.0): record the double defence against tool confusion (T-141)
3cc1fac53188 -> 29607fe929c8  feat(mcp-gateway): T-142 — remote.file.list completes the file family (ADR-0071)
120a8bd0dcdd -> 9aa36997c8ae  docs(v0.14.0): T-142 review — mutation fidelity, and a corrected brief
dc4690be9db5 -> a0a6da8193b8  feat(web,ui): T-143 — the approval prompt describes the tool it is approving (FE-43/44/45)
8aaeecc79c07 -> 64b99e972ffe  fix(web,ui): T-143 review — plain-language mutating copy, and the read/list disclosure line D3 implied but the prompt never stated
87687f65d4d0 -> f4392f1f73b4  build(infra): T-146 VER-23 -- move the offline/e2e/repro builder to golang:1.26
01701f603558 -> db980a1fa1ae  build(lint): T-146 VER-25 -- migrate golangci-lint to v2.13.0, fix 9 findings
7c764dacbcf3 -> 03c4d7f85b42  docs(adr): T-146 VER-28 -- ADR-0072 supersedes ADR-0041's toolchain-version clause
5cf03e5edb33 -> 0626c3414bde  docs: sync the ADR head to 0072 after T-146
791eed1043c7 -> 9f8ffb2f899d  build(release): T-144 OI-44 -- bump BUNDLE_VERSION to 0.14.0 before capstone build
ea39efcb0914 -> 5f6ac3eaf475  fix(e2e): T-144a Task 2 -- update WD8 N2's stale tool-surface set for the v0.14.0 file family
5c0d8d46ad15 -> 7e9877beac9d  test(e2e): T-144a Task 3 -- live e2e leg for the governed file family (AUD-41/42/43)
de69ce89175c -> e8916f9e33a1  docs(v0.14.0): T-144 capstone results — all gates green, two carry-forwards
4b61b2dae8dd -> 31ed54a77740  docs(v0.14.0): T-144b — SEC-52 cumulative security review
df77561e64ba -> 98a4ca40c9f0  fix(gateway,release): T-145 -- R4 stale-.sig rebuild fix + SEC-52-1 provenance consistency
0f3cb7b94e15 -> b05a292bc935  docs(v0.14.0): T-145 -- VER-21 (F-1 closed) + DOC-37/38/39/40 + release closure
6b72739d7494 -> a06d71c5bd51  docs(v0.14.0): VER-19 PASSED — the CEO §human pass, with two findings
e6dc53f0271d -> 75b568c2d8b7  docs: reconcile the shipped state — v0.14.0 RELEASED + TAGGED a06d71c
a14ef307e2a0 -> 28892823e68c  docs: ADR-0073 product boundary + version namespaces + resolve 3 stale Drafts
90dbfdf0902f -> 06b46f998014  chore(docs): Pass 1 staleness sweep + mechanise the class as a gate
6131913a7b9e -> 710cf13e5aeb  docs(adr): Pass 2 — generated ADR status ledger, template, and its gate
534356243ce7 -> b8bda37bd03d  docs(executive): Pass 3 — rewrite the executive tier to conform to ADR-0002/0073
2c6b881db83f -> 53e4eaecb678  docs: add verification-discipline rules 11-12 to CLAUDE.md
89e8d09fdeec -> 0a0398c9d040  chore: close the three recorded gaps — go-deps gate, references CSV gate, openapi ruling
0b07fedc2e85 -> a985b1d4398f  docs: name go-deps-check in CLAUDE.md's gate list
286eb2ef1fa6 -> ffe67e660358  docs(adr): correct ADR-0073's dependency count in place; record the edit-vs-supersede rule
766472b40f6f -> 920866908f25  docs(v0.15): scope "Governed Decision Policy" — ADR-0074, criteria, WD-15, T-147..T-154
4f5aec179fda -> 6b794f46a3ae  docs(canon): correct CANONICAL's own 'last updated' stamp to 2026-08-28
7ba4b6396896 -> 0633fd314b70  docs(adr): ACCEPT ADR-0074; fix the ledger's status parser it exposed
c9843a4637d0 -> 3e05ee9bf3b8  feat(policy-service): add policy_rules store (ADR-0074, T-148)
0963eaac9fc9 -> bd263cc3f2d4  docs(v0.15): record VER-37 — 26 down migrations exist, none can be run
ca7a43a9824b -> 92154be9c616  docs(v0.15): re-cut T-149/T-150 on audit evidence; add CON-6 and SEC-58
9b763ec5135a -> 638d4c012c7b  feat(policy-service): add the policy rule evaluator as a pure function (ADR-0074, T-149a)
cbfbdc661bd3 -> 37d58c33c98f  docs(v0.15): T-149a review notes — T-150 fail-closed on unknown outcomes; re-price the ladder
5f7a8f825104 -> c692f73e0e0d  docs(v0.15): T-149b audit — the fail-closed DEFAULT is the real hardening
2c3e90aa40dc -> ae4e1aef6a57  feat(v0.15): T-149b — callers refuse a policy deny (SEC-58)
219ce38fe382 -> 44d1acc3afac  docs(v0.15): T-149b review — AUD-38, a denial must not be recorded as an outage
67869d7a900b -> 8c9cd8bb99e1  docs(v0.15): ratify the T-150 split, ruling (b), and PERF-9's scope
48e400b1d7a4 -> 14d756d9e921  docs(v0.15): second T-150 audit — AUD-39, the default audit lens changes meaning
fa7b06229760 -> accdaafddead  docs(v0.15): correct CON-1's contradiction with AUD-35; record PERF-10
f1ecb6fbd3f7 -> 26c753fe0cb5  feat(policy-service): T-150 switch-on — decideHandler consults the evaluator
b457c7356c34 -> afc7b0c47086  docs(v0.15): VER-38 — four integration suites the release gate never runs
6e615d7908ed -> 85dc06b15c0c  docs(v0.15): ratify T-150b's mechanism, OI-38; record a withdrawn false alarm
d8b444776a1c -> 1e2109a19195  feat(api): T-150b — consult policy on every session-start (CON-6, AUD-38, OI-38)
600843e92773 -> 74d54a5b51bf  docs(v0.15): T-150b review — SEC-59 (an ungoverned session path) and PERF-11
0e89dd1b0f2c -> 8ed1ff9b1f19  docs(v0.15): split T-151a/b; ratify SEC-53's mechanism and /v1/policy-rules
8fb7415564fd -> 8f2da0c4c948  T-151a: service-service CRUD routes for policy_rules on policy-service
985a8a87b601 -> 479f0d7a0108  docs(v0.15): T-151a review — CON-7 (malformed id), and my estimates are noise
e978bb3158b0 -> f9cc24bbe5fe  docs(v0.15): AUD-40 — rule authorship is unaudited; add T-151c and authoredBy
78917ae08aa0 -> c4d2d88e3b6a  feat(api): T-151b — /v1/policy-rules operator surface (SEC-53, CON-5, CON-7, VER-33)
9253d10d9f28 -> 09aefa5b4fdd  docs(v0.15): VER-39/40 — two environmental failure modes found verifying T-151b
7df5043c5887 -> 6849b93801bb  docs(v0.15): T-151c gains SEC-59 + a VER-38 slice; fix the authoredBy gap
7e8e934134a2 -> d42260b12836  feat(policy): T-151c — chain rule-authorship events (AUD-40), close SEC-59, gate VER-38 slice
c841b639b286 -> b34bb3fe819c  docs(v0.15): AUD-41 — the rule event overloads Edge, a filtered field
153fb26902b9 -> 22ba44f0734a  docs(v0.15): FE-25a — the T-152 audit says the blast radius must render INLINE
696791a78231 -> 9b01cc6e779e  feat(web): T-152 — policy rules UI, blast radius rendered inline (FE-24, FE-25 block)
8dd5fcfbe6a4 -> 9b4fd9448032  docs(v0.15): FE-27 — /policy-rules is undiscoverable, and it blocks the §human pass
9e933096bb4b -> ede0d1620eb7  docs(v0.15): fold FE-27 + AUD-41 into T-152b; correct AUD-41's shape
e25993532692 -> 414fb97fed87  fix(web,policy): T-152b — FE-27 makes /policy-rules discoverable, AUD-41 stops overloading Edge
7b5a4317c723 -> eb5d1801f02f  docs(v0.15): SEC-53a — a reported paired-session gap, checked and FALSE
42b0e2da0381 -> c4ace10b5342  docs(spike): T-153 — the inference-substrate measurement (ADR-0073 §3(a))
1cb796eb187a -> 155050c6ace1  docs(spike): make T-153's cleanup note precise about what was actually verified
82efa185b696 -> 7ec69b4af0d5  feat(v0.15): T-154 capstone — WD-15 live proof, VER-29..31/CON-1 verified, security review
e05eaf565f8b -> 178b26392dc7  docs(v0.15): rule on SEC-57 — write the test; file T-155 with a second proof
b31bb7ee026e -> 49464c8f7cef  test(v0.15): T-155 — the two missing negative proofs (SEC-57 + WD-15 mutation control)
eb385f51404d -> 35c21f42802c  fix(v0.15): T-155 — correct SEC-57's control leg to actually control
53782dde02ef -> a37da1eca8f4  release(v0.15.0): VER-32 passed — bump the release claims to 0.15.0
8126d348dbfa -> 4bb3646ba514  docs(v0.15.0): closure — publish provenance, record the tag, fix the runbook
8a942a896dad -> a42cb0b4f05f  docs(spike): T-153 air-gap leg MEASURED — the runtime is a closed unit
e895636219c2 -> 91692aad5c66  docs: balance the bold markers in CLAUDE.md's v0.15.0 release line
0cfbb170f23e -> eb4c16dec642  docs: retire the stale v0.15.0 release-target line; record the open sequencing question
9d0df4c2c05b -> b4bc2792e490  docs: scope v0.16.0 "Public Readiness" + analyse B-041's remaining work
e6256d2a3435 -> 8deb5b0f42dc  docs(adr): ADR-0075 licence (Proposed) + ratify splitting the surface freeze
782d3009217e -> ce6dcecfa763  docs: ADR-0075 ACCEPTED (Apache-2.0); v0.16.0 RATIFIED; sequencing closed
4010a2060c8c -> 0f6bb9faf810  docs(adr): ADR-0076 ACCEPTED — DCO, not a CLA (discharges ADR-0075's deferred item)
95f0100d0c5e -> 24ea09f60e12  docs: resolve a canon contradiction I created about the surface freeze
0d7be6382d9b -> 46ba39d198e4  docs(v0.16): name both binding ADRs; restore PUB-7/PUB-8 ordering
1541b8ab5d09 -> c0a3dc422a93  feat(t-157): content-level git-history secret scan + negative control (PUB-1, SEC-60)
fd5d7abecd38 -> b1ce3a831be0  fix(t-157): discriminate conn_string_credential at the credential VALUE (Commander review)
44d21c2b6598 -> 5ff3fb51ca88  docs(v0.16): discharge PUB-1/SEC-60/T-157 rows with the real outcome
5b9149ad0a7d -> a6c6c04dce3a  feat(t-158): block-criterion coverage gate + mutation proof (VER-41, SEC-61, VER-44)
82f8747ee89b -> a34c671489da  fix(t-158): prove the gate discriminates + record the release-check python3 finding (Commander review)
92c3a28dbd15 -> 28c666e5c298  feat(t-159): close VER-38/VER-42 by deriving the integration-suite list
9173da143742 -> 563a1040f9a4  feat(t-160): extend doc-check to docs/releases/ + docs/requirements/*.md (VER-43, SEC-62)
10f677a7735f -> 1b7882231b6e  fix(t-160): correct the break-invisibility reason + record VER-48 (Commander review)
b5f6a8e9748a -> 710d04600619  feat(t-161): public-facing docs — README/SECURITY/CONTRIBUTING/LICENSE (PUB-2/3/4/5/8)
6a4af4ed1fa3 -> b08772dfccef  docs(readme): replace internal provenance markers with plain links
bbf3c5c698fe -> dba62b24484b  fix(t-162): retro-publish v0.13/v0.14 fingerprints, resolve DOC-17, close VER-45
af85787c193a -> b377ee151b9a  fix(sign-release): the mixed-version guard's message named an escape hatch that does not exist
8bc5ecfee21b -> ca18f0892c10  release(v0.16.0): T-163 capstone — VER-46 green on both stacks, DOC-16, artifacts built
fe4c5bd5f0d3 -> 142525769978  fix(v0.16.0): remove artifact hashes from release notes — Commander review
8faec48a6b71 -> e9ed88b71956  release(v0.16.0): resolve both publication blockers; record PUB-9
a15dc4b0c724 -> e908b8c1ac3d  docs: name this repo as the ecosystem's CORE; correct a stale-canon warning
43829294aaf3 -> bd4f7d04388a  docs(v0.16.0): fix every PUB-6-rehearsal defect; state the postgres limitation
e7c14ee2bd33 -> 6e39f81a5168  docs(adr): ADR-0077 (Proposed) — a self-contained PostgreSQL companion
b13da2f62d1d -> 767e0f0a3853  docs(v0.16.0): record the CEO's PUB-6 run — product passed, documents failed
30656963a3e9 -> e4b62f7f78ab  fix(install): restart units on re-run, validate DB URL + resource id, line editing (T-165)
3463b748493f -> d5a178b948cc  fix(install): plain wording for the failed-restart message (T-165 review)
f1480ed195d5 -> 618910e26d1b  docs(v0.16.0): T-165 done — evidence + deferred health-check residual
83ce606823af -> 574136988305  fix(web): cockpit onboarding a stranger can follow (T-166, PUB-6 P10/P12/P13/P15a)
acff230a087c -> d7b548f31c67  docs(v0.16.0): T-166 done — evidence + unrun quickstart-a assertions flagged for the re-cut
420df31e2575 -> 2d7f066edfec  docs(install): user-first install guide, one verification place, quickstart that matches what ships (T-164, PUB-6)
335c99b36464 -> 7cf1456c8a8b  fix(doc-check): fenced code blocks hid inline path references; gate the user guides
c0435aa943e7 -> 3d0a805df919  docs(v0.16.0): T-164 done + the doc-check fence defect found at its review
7ca592f363e9 -> e43cece6bc33  docs(v0.16.0): correct 'no runtime change' — the PUB-6 fixes change installer re-run + onboarding display
e808c01b5924 -> 8c353e79fdf4  docs(v0.16.0): closure — PUB-6 passed on re-run, tagged e43cece, hashes + fingerprint
99978288b63c -> 03f4646d3a5d  docs(canon): remove the last stale 'sign -> PUB-6 -> tag' clause (closure follow-up)
82f6784ecadd -> 55b28fcbda33  fix(history-scan): allowlist the exact USER:PASSWORD template pair (pre-push PUB-1 run)
```
