# Agent Workflow Compiler

## Purpose
Compile markdown/spec-driven workflows into safe executable runs for CI/CD, local runners, or sandboxed workspaces.

## Input
- YAML frontmatter
- natural language instructions
- tool allowlist
- schedule or trigger
- sandbox profile
- evidence requirements

## Output
- executable workflow definition
- policy manifest
- audit hooks
- evidence contract

## MVP implementation
Start with internal YAML workflow files. Later support GitHub Agentic Workflow style markdown and self-hosted runner compilation.
