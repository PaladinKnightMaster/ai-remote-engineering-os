# Memory and Context Architecture v3

## Three-layer context system
1. Live Index - continuously fresh repo/docs/tickets/messages.
2. Persistent Memory - user/project/org/ticket/run/skill/workstream memories.
3. Reasoning Retrieval - vector search, graph search, and tree/page reasoning.

## Memory controls
- export
- redact
- expire
- pin
- audit
- confidence
- source trace
- permission scope

## Storage pattern
Use filesystem-friendly memory manifests plus database indexing. Avoid hidden-only memory.
