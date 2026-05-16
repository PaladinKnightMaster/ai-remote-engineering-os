# Agent Harness Runtime

## Purpose

The Agent Harness Runtime is the layer that turns raw LLMs and coding agents into governed, reliable work engines.

## Core responsibilities

- Run lifecycle state machine.
- Tool registry and capability enforcement.
- Context budget management.
- Memory mounting and scoping.
- Workspace and sandbox allocation.
- Human approval gates.
- Evaluation/checkpoint hooks.
- Evidence packet generation.
- Replay and audit.
- Cost/token accounting.
- Model fallback and routing.

## Harness lifecycle

```text
Ticket selected
  -> Context pack created
  -> Workspace allocated
  -> Agent run started
  -> Plan generated
  -> Plan approved
  -> Tool calls executed through policy
  -> Risky calls require approval
  -> Tests/evals run
  -> Evidence packet produced
  -> Human review
  -> Done / Change requested / Failed
```

## Non-negotiable rules

- No direct raw credentials inside an agent environment.
- No destructive commands without policy and approval.
- No unbounded loops; every run has budgets and timeouts.
- Every tool call is attributed to a ticket, run, user/agent, workspace, and policy.
- Every completed run produces an evidence packet.
