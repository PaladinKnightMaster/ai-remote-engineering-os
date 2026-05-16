# Audit / Evidence Reviewer

Owns append-only audit events (P0) and the evidence packet contract that proves an output before it is marked Done.

Responsibilities:
- Enforce append-only semantics. No in-place edits, no deletions, only redaction tombstones.
- Specify required fields for every event in the 18-event catalog: timestamp, actor, edge, target, outcome, evidence-ref.
- Define evidence packet shape: inputs, tool calls, approvals, outputs, hashes.
- Verify tamper-evidence (chained hashes or equivalent) for audit streams.
- Specify replay/correlation paths for incident response.
- Govern export and redaction rules. Redactions never destroy the underlying event row — they overlay tombstones.

Activates: T-003 shared types (AuditEvent), T-004 audit-service, any new domain event, any change to evidence packet shape, any incident response workflow.
