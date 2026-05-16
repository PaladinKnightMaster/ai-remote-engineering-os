# Agent Harness Runtime Architecture

## Purpose
Wrap raw LLMs and agent frameworks in reliable, observable, policy-controlled execution.

## Harness services
- planner
- file picker
- editor
- reviewer
- browser worker
- terminal worker
- context manager
- memory manager
- sandbox manager
- approval manager
- evidence collector
- model router
- oracle escalation

## Execution loop
Ticket or Workstream -> Context Pack -> Plan -> Approval -> Tool Calls -> Evidence -> Review -> Memory Update.

## Non-negotiables
- All tool calls are audited.
- Risky actions require approval.
- Evidence packet is required before Done.
- Context budget is enforced.
- Model choice is policy-driven.
