# Model Gateway and Local AI Profile

## Purpose

The system must support frontier models for speed and quality when internet is allowed, and local/company-hosted models when working inside restricted networks.

## Model profiles

```text
Frontier BYOK profile
  OpenAI, Anthropic, Gemini, other external APIs.

Company-hosted profile
  LiteLLM, vLLM, Ollama, LM Studio server, NVIDIA NIM, internal OpenAI-compatible endpoint.

Laptop-local profile
  Ollama or LM Studio running on the same device.

Perception profile
  Multimodal/omnimodal models for screen/video/audio/document understanding.
```

## Selection strategy

- Use frontier APIs for complex planning/coding when allowed.
- Use local/company models for confidential/offline work.
- Use small fast models for classification, routing, summarization, and UI state.
- Use multimodal models for future GUI/video/speech perception.
- Never make one model mandatory.

## Local AI setup profile

Support docs and config for LM Studio, Ollama, Continue in VS Code, local OpenAI-compatible endpoints, model capability checks, and tool-calling support checks.
