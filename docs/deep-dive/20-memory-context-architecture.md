# Memory and Context Architecture

## Purpose

Agents need persistent context, but enterprise users need inspectability, exportability, redaction, audit, and scoped permissions. The memory system must be product-grade, not a hidden embedding cache.

## Memory scopes

```text
Org memory       read-mostly shared knowledge
Team memory      team conventions and recurring decisions
Project memory   product specs, architecture, ADRs, runbooks
Repo memory      code graph, symbols, tests, dependencies
Ticket memory    clarifications, plan, constraints, outcomes
User memory      private preferences and working style
Agent memory     skills, observations, reusable methods
Run memory       transcripts, tool calls, evidence
```

## Storage model

Use a hybrid model:

- Filesystem-style memory for inspectable notes and structured artifacts.
- Relational metadata in PostgreSQL.
- Object storage in MinIO for larger artifacts.
- Optional vector index for retrieval.
- Optional code graph for structured repo context.

## Context pack

A context pack is the bounded input bundle for an agent run. It includes ticket summary, acceptance criteria, linked docs, relevant code graph excerpts, memory snippets, constraints, policy, allowed tools, evidence requirements, and token/cost budget.

## Rule

Memory must be user-visible, versioned, redactable, exportable, and auditable.
