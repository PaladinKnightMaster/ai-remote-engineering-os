# Agent Browser CLI Architecture

## Purpose
Provide auditable, command-oriented browser automation.

## Strategy
- prefer DOM/CDP/Playwright structured control
- return JSON snapshots, logs, screenshots, and audit records
- avoid unbounded browser freedom
- attach browser evidence to tickets/runs

## Commands
open, snap, click, type, wait, logs, screenshot, audit, close.
