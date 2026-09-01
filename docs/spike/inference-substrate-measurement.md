# Inference-substrate feasibility measurement (T-153)

**Measured 2026-08-31.** Discharges **ADR-0073 §3(a)** — the feasibility measurement the product
boundary requires before the platform may ship any substrate. Shaped after **ADR-0052 §OI-c**, whose
standard is *build it and measure the number against a stated cap*, not survey.

> ⚠ **THIS DOCUMENT ADOPTS NOTHING.** It ships no code, adds no dependency, and decides nothing.
> ADR-0073 §3 requires **(a)** a measurement *and* **(b)** its own ADR before adoption. This is (a).
> Nothing here authorises (b).

## Why it was taken now

v1.0.0's flagship (**B-041**) integrates OpenHands SDK and openworker. **Both need a model to run**,
and under ADR-0073 §2 a model runtime is an **operator-provided substrate**. Until that resolves,
**v1.0.0 cannot be demonstrated in the `lan-only` profile** — the one segment where the air-gap claim
is unmatched.

Every prior claim about an inference substrate was `[inherited:]`. `references/references_v4.csv`
says so itself: verification of the v7 intake is *README-level*, and *"any row gating real spend
(MAGNITUDE above all) needs the deep pass first."* This is that pass, for the feasibility half.

## The cap being measured against

| | |
|---|---|
| **OI-4 budget** | **≤ 150 MB gzipped**, ≤ 500 MB uncompressed [read: `docs/releases/v0.1.0/v0.1.0-release-criteria.md:274`] |
| Core bundle today | **44.7 MB gz** [read: `docs/releases/v0.14.0/v0.14.0-release-criteria.md:456`] |
| **Headroom** | **≈ 105 MB gz** |

## Measurements

All sizes are **compressed** bytes as published by the registry or release host — the number an
operator actually transfers. Method: registry/API manifest inspection, which yields exact artifact
sizes without downloading them.

### The runtime

| Artifact | Compressed | Note |
|---|---|---|
| `ollama/ollama:latest` (linux/amd64) | **3,226.5 MB** | 4 layers; bundles GPU runtimes [ran: `docker manifest inspect`] |
| `ollama-linux-amd64.tar.zst` v0.33.2 | **1,356.4 MB** | the bare Linux x86-64 release [ran: GitHub releases API] |
| `ollama-linux-amd64-rocm.tar.zst` | 999.6 MB | AMD-GPU variant |
| `ollama-linux-arm64-jetpack6.tar.zst` | 255.9 MB | NVIDIA-Jetson-specific |

The container image is measured **and then set aside**: quoting only the 3.2 GB figure would be a
strawman, since it packages CUDA/ROCm. **1,356.4 MB is the fair number** for a general-purpose Linux
host.

### The models

Exact layer totals from the Ollama registry [ran: `curl registry.ollama.ai/v2/library/<n>/manifests/<t>`]:

| Model | Compressed |
|---|---|
| `gemma3:270m` | **278.0 MB** |
| `qwen2.5:0.5b` | 379.4 MB |
| `llama3.2:1b` | 1,259.9 MB |

## The arithmetic

Smallest defensible pair — bare amd64 runtime plus the smallest model measured:

```
1,356.4 MB  (ollama-linux-amd64)
+   278.0 MB  (gemma3:270m)
= 1,634.4 MB   vs a 150 MB cap and ~105 MB of headroom
```

- **≈ 10.9× the entire OI-4 bundle cap**
- **≈ 15.6× the available headroom**

⭐ **And the decisive line, which needs no runtime at all: the smallest model measured, 278.0 MB, is
already 1.85× the whole bundle cap and 2.6× the headroom.** Even shipping *only* weights, with the
runtime supplied entirely by the operator, does not fit.

**Verdict: an inference substrate cannot ship inside the core offline bundle.** This is the same
conclusion, and the same shape of conclusion, that ADR-0052 §OI-c reached for postgres — measured,
not argued.

## ⭐ The sharper question: does ADR-0052's companion-artifact pattern absorb this?

Answering only "does it fit in 150 MB" would be cheap. The useful question is whether the pattern the
project **already has** — per-OS companion artifacts shipped *outside* the core bundle
(`aireos-postgres-<ostag>.tar.gz`, ~80–85 MB each) — carries inference too.

**Same shape, yes:** a per-platform artifact, fetched separately, installed beside the core bundle.
Ollama even ships per-architecture variants (amd64 / arm64 / rocm / jetpack) exactly as postgres
ships per-OS closures.

**But two differences are categorical, not merely larger:**

1. **An external dependency the artifact cannot contain.** The rocm/jetpack variants exist because
   GPU acceleration needs **host drivers that live outside the artifact**. A postgres companion is a
   closed dependency closure: install it and postgres runs. An inference companion is not closed —
   its useful performance depends on a host GPU stack the bundle can neither ship nor verify. On CPU
   only, it is closed but slow, and "slow" is a product question, not a packaging one.
2. **Redistribution terms attach to the weights.** Postgres `.deb`/`.rpm` files are redistributable
   under known terms. **Model weights carry their own licences** (Llama community terms, Gemma terms,
   and so on), and redistributing them inside our artifacts is a legal question the postgres
   companions never raised.

**So: the pattern carries the runtime awkwardly at ~16× a postgres companion, and does not obviously
carry the weights at all.** The honest reading is that ADR-0073 §2's default — **operator-provided,
governed at the seam** — is not a temporary concession pending a better package. It is the
architecturally correct answer, and this measurement supports it rather than undermining it.

## Air-gap behaviour after download

*(This section records the second-order question ADR-0052 §OI-c surfaced for postgres — where
`apt-get --download-only` + `dpkg -i` turned out **not** to be a self-sufficient closure. The
analogue here is whether the runtime reaches the network on first run.)*

✅ **MEASURED 2026-09-01. The runtime is a self-sufficient closure: it serves inference with the
network severed.** *(Three earlier attempts failed environmentally — a Docker named-volume
`input/output error`, a harness timeout whose wrapper still exited 0, and a hung daemon. All three
root-caused to host disk exhaustion, not to the runtime. Recorded because the record briefly blamed
the volume driver, which was a symptom read as a cause.)*

**Method** — deliberately the *bare-binary* path, not the container image: the 8.45 GB
`ollama/ollama` image bundles CUDA/ROCm, and nobody ships that to an air-gapped host. Installed
`ollama-linux-amd64.tar.zst` v0.33.2 into a plain `ubuntu:24.04` container — **the download measured
1.4 GB on the wire, corroborating the 1,356.4 MB registry figure above, which was obtained by manifest
inspection without downloading anything.** Pulled `gemma3:270m` (291 MB on disk vs the 278.0 MB
compressed figure) with network available, then **removed the container from the Docker network**.

⭐ **The severance was proven before inference was attempted, with a discriminating control** — without
this the result is unfalsifiable, since a container still quietly attached to the bridge produces an
identical answer:

| Same probe → `registry.ollama.ai` | Result |
|---|---|
| **while connected** | **HTTP 404** — a real server response; TCP + TLS + HTTP all completed |
| **after `docker network disconnect`** | **HTTP 000** — no connection established at all |

**Then, with the network severed** [ran: `POST /api/generate`, `gemma3:270m`, `stream:false`]:

```
"response":"The name AIRGAP-OK is a popular and well-known name ...","done":true,"done_reason":"stop"
```

Real generated tokens — a **positive** marker, not an exit code. (The CLI path was tried first and
returned exit 0 with nothing but TTY spinner frames; that exit was `head`'s, not the model's, and was
**not** accepted as evidence. The HTTP API was used precisely to get an assertable payload.)

⚠ One near-miss worth recording: a `grep` of the server log for `dial|refused|no route|lookup|timeout`
returned **1 match**, which reads as a network attempt. It was a **false positive** — the string
`timeout` inside `OLLAMA_LOAD_TIMEOUT:5m0s` in a startup config dump. Reporting the count without
reading the line would have manufactured a finding.

⭐ **The result, and why it matters: the second-order concern does NOT materialise.** ADR-0052 §OI-c
found that postgres's `apt-get --download-only` + `dpkg -i` was **not** a self-sufficient closure. The
analogous worry here — that the runtime phones home on first inference — is **false**. Runtime plus
weights is a closed unit for inference.

**So the barrier is purely SIZE AND DISTRIBUTION, not runtime behaviour.** That strengthens rather
than weakens ADR-0073 §2: an operator genuinely *can* provision this out-of-band (1.4 GB runtime +
291 MB model) and then run it disconnected. **A `lan-only` v1.0.0 demo is therefore possible** — as
operator-provisioned substrate, never as bundled payload.

⚠ **Scope of this leg:** CPU path only (no GPU passthrough), one small model, one runtime, a single
inference. It establishes that no network is *reached*; it measures nothing about latency, throughput,
or quality — see below.

## What this measurement does NOT establish

- **It does not evaluate `magnitude`**, the candidate ADR-0073 §3 names. Ollama was measured because
  it is obtainable and representative of the class; the order-of-magnitude finding above is a
  property of *model weights plus a runtime*, not of one vendor. A `magnitude`-specific pass is still
  outstanding if it is ever proposed for adoption.
- **It does not measure inference quality, latency or hardware requirements.** Only distribution size
  and air-gap behaviour.
- **It does not authorise adoption.** ADR-0073 §3(b) still requires an ADR.

## Status

- ✅ **Size measurement: complete and decisive.** ADR-0073 §3(a) is discharged for the bundling
  question: an inference substrate **cannot ship in the core offline bundle**, by roughly an order of
  magnitude, and the companion-artifact pattern does not cleanly absorb it.
- ✅ **Air-gap run: MEASURED 2026-09-01 — the runtime serves inference with the network severed**,
  severance proven first with a discriminating before/after control. The ADR-0052 second-order worry
  does not materialise: runtime + weights is a closed unit. **The barrier is size and distribution
  alone**, which makes an operator-provisioned `lan-only` v1.0.0 demo viable.
- ⚠ **Host cleanup: probably already clean, but UNCONFIRMED.** A sweep found **no `ollama` image and
  no `ollama-probe` container** — but that sweep ran while the daemon was already degrading
  (`docker info` returned `124`), so it is **not trustworthy evidence**. Re-check once Docker is
  healthy: `docker images | grep ollama` and `docker ps -a | grep ollama-probe`. Recorded this way
  deliberately — "a check that ran against a sick daemon said nothing was there" is not the same
  claim as "nothing is there", and collapsing the two is how a 3 GB artifact gets left on a host.
