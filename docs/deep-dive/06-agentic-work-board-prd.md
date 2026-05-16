# PRD - Project C: Agentic Work Board

## Problem

Agent chats are not a reliable system of record. Teams need tickets that can own goals, plans, agent runs, approvals, evidence and review state.

## Product statement

A lightweight Jira/Linear/Plane-style board where humans and AI agents collaborate through tickets. The ticket is the control plane.

## MVP states

```text
Inbox -> Clarify -> Planned -> Ready for Agent -> Running
-> Needs Approval -> Evidence Review -> Human Review -> Done
```

Additional states:

- Blocked
- Failed
- Parked
- Ready to Merge
- Ready to Deploy

## Ticket data

- title
- type
- priority
- owner
- state
- acceptance criteria
- context links
- allowed tools
- model profile
- workspace policy
- evidence requirements
- agent runs
- approvals
- artifacts
- comments
- audit events

## Agent run lifecycle

1. Human creates ticket.
2. Agent/human clarifies missing requirements.
3. Agent generates plan.
4. Human approves plan.
5. Workspace allocated.
6. Agent run starts.
7. Risky tools request approval.
8. Evidence packet generated.
9. Human reviews.
10. Ticket closes or returns to work.

## Evidence packet

Every completed run should produce:

- summary
- changed files list
- diff/patch
- terminal transcript
- test/lint/build results
- screenshots/video if GUI involved
- known risks
- follow-up tickets

## MVP scope

- Board and ticket CRUD.
- State machine.
- Agent run timeline.
- Approval requests.
- Evidence packet.
- Mobile approval view.
- Link to Remote Cockpit session.
- Link to Agentic Desktop run.

## Future scope

- Scrum/Kanban/Lean/SAFe.
- Epics, sprints/cycles, releases and roadmaps.
- External tracker connectors.
- Automation rules.
- Metrics and dashboards.
