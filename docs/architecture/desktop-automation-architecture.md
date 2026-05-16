# Desktop Automation Architecture

## Priority order
1. OS accessibility tree
2. Browser DOM/CDP/Playwright
3. OCR/vision/screenshot interpretation
4. Raw keyboard/mouse fallback

## Why accessibility first
Structured accessibility trees provide deterministic element references and reduce token-heavy screenshot reasoning.

## Governance
Desktop automation always runs through session policy, approval gates, and audit recording.
