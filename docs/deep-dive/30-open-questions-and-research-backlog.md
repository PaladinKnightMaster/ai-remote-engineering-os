# Open Questions and Research Backlog

## Product questions

- Which ICP is first: internal IT, secure engineering teams, labs, or regulated companies?
- Should the first alpha be desktop-first, browser-first, or board-first?
- Which deployment profile should be default: LAN, VPN, or DMZ?

## Engineering questions

- Go vs TypeScript for the control plane.
- Tauri vs Electron for Agentic Desktop.
- Guacamole-only MVP vs MeshCentral-first MVP.
- PostgreSQL event table vs dedicated event store.
- Open Policy Agent vs custom policy service.

## AI questions

- Which local model profile is the first supported baseline?
- How to benchmark context packs and memory quality?
- How to evaluate safe tool-use reliability?
- How to route between frontier and local models automatically?

## Security questions

- How to design short-lived capability tokens.
- How to verify no secrets enter prompts, logs, memory, or screenshots.
- How to record GUI sessions without leaking sensitive data.
- How to handle regulated deployments and audit exports.
