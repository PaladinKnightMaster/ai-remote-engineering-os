# Token Budget and Context Pruning

## Problem

Agentic coding can consume enormous token volumes. Context also degrades reasoning when it is noisy or too large.

## Requirements

- Every run has a token budget.
- Every context pack has a visible size estimate.
- Tool outputs are summarized or filtered before entering context.
- Large logs are stored as artifacts and referenced by extracts.
- Code context is retrieved through graph/search, not pasted wholesale.
- Repeated failures trigger context compression and human escalation.

## Context budget manager

```text
Inputs:
  ticket, docs, memory, code graph, prior runs, tool output

Outputs:
  bounded context pack, retrieval policy, summarization policy, model choice
```

## Metrics

- Input tokens by source.
- Output tokens.
- Tool output tokens suppressed.
- Cost per ticket.
- Agent run retries.
- Context-pack hit/miss quality.
