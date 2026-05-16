# Living Workspace / Workstream Architecture

## Purpose
Convert recurring work into managed workstreams instead of isolated chats.

## Key objects
- Workstream: recurring or long-lived objective.
- Workspace: stateful environment with files, rules, context, tools, memory, dashboards, and history.
- Run: one execution attempt.
- Evidence Packet: proof of output.
- Review: human acceptance or correction.

## Lifecycle
1. Define workstream objective.
2. Attach sources, rules, tools, memory scope, and schedule.
3. Execute run in a workspace.
4. Collect evidence.
5. Review and add corrections as future rules/memory.
6. Schedule next run.

## Examples
- Weekly competitor research.
- Daily flaky test triage.
- Release note drafting.
- Security advisory monitoring.
- Customer feedback summarization.
