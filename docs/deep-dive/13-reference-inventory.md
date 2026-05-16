# Reference Inventory
This document preserves every user-provided reference URL plus key external architecture references used during planning.
| Name | URL | Tags | Status | Phase | Use |
|---|---|---|---|---|---|
| Agent of Empires | https://github.com/njbrake/agent-of-empires | `[REMOTE][TERMINAL][MOBILE][SANDBOX]` | Reference | MVP inspiration | Terminal-first multi-agent/session dashboard; use ideas for tmux persistence, QR/passphrase access, git worktrees, Docker sandboxing. |
| Apache Guacamole | https://guacamole.apache.org/ | `[REMOTE][GUI]` | Candidate dependency | MVP | Clientless browser remote desktop gateway for RDP/VNC/SSH. |
| MeshCentral | https://github.com/Ylianst/MeshCentral | `[REMOTE][GUI][DEVICE]` | Candidate dependency | MVP | Self-hosted device management with web remote desktop, terminal, and file management. |
| RustDesk OSS | https://rustdesk.com/docs/en/self-host/ | `[REMOTE][GUI][RELAY]` | Optional backend/reference | Post-MVP | Self-hosted ID/rendezvous and relay infrastructure for RustDesk clients. |
| Pangolin | https://docs.pangolin.net/ | `[REMOTE][DMZ][VPN][ZTNA]` | Candidate dependency | MVP | Open-source identity-based WireGuard remote access/reverse proxy. |
| OpenZiti | https://netfoundry.io/docs/openziti/learn/introduction/ | `[REMOTE][ZTNA][SECURITY]` | Candidate dependency | Post-MVP/high-security | Open-source zero-trust overlay network with controllers, routers, tunnelers and SDKs. |
| NetBird | https://docs.netbird.io/selfhosted/selfhosted-guide | `[VPN][REMOTE][SELFHOST]` | Candidate dependency | MVP deployment profile | Self-hosted WireGuard management/signal/relay/COTURN architecture. |
| Headscale | https://headscale.net/ | `[VPN][SELFHOST]` | Optional dependency | MVP deployment profile | Self-hosted Tailscale control server; narrow single-tailnet scope. |
| Superpowers | https://github.com/obra/superpowers | `[SKILLS][WORKFLOW][QUALITY]` | Reference | Dev process / agent phase | Structured spec-plan-implementation-review workflow. |
| Routa | https://github.com/phodal/routa | `[BOARD][ORCH][EVIDENCE][DESKTOP]` | Reference | Agentic Work Board | Workspace-first multi-agent coordination with tasks, sessions, traces, evidence and review visible on board. |
| CubeSandbox | https://github.com/TencentCloud/CubeSandbox | `[SANDBOX][SECURITY]` | Future evaluation | Future | Sandbox infrastructure for AI/container workloads; evaluate after MVP. |
| OpenMythos | https://github.com/kyegomez/OpenMythos | `[RESEARCH]` | Reference only | Future research | Theory/reconstruction reference; do not base MVP assumptions on it. |
| GenericAgent | https://github.com/lsdefine/GenericAgent | `[GUI][DESKTOP][SKILLS][RESEARCH]` | Reference | Future | Computer-control/self-evolving skill reference; high-risk inspiration only. |
| Multica | https://github.com/multica-ai/multica | `[ORCH][BOARD][SKILLS]` | Reference | Agent phase | Managed coding-agent lifecycle, skills, task assignment. |
| pi-mono | https://github.com/badlogic/pi-mono | `[MODEL][TERMINAL][OBSERVE]` | Reference | Agent phase | Toolkit ideas: coding CLI, unified model API, TUI/web UI, Slack bot, vLLM pods. |
| opencode | https://github.com/anomalyco/opencode | `[DESKTOP][TERMINAL][AGENT]` | Adapter candidate | Agent phase | Open-source coding agent candidate behind adapter interface. |
| Thunderbolt | https://github.com/thunderbird/thunderbolt | `[MODEL][LOCAL][PRIVACY]` | Reference | Agent phase | Local/private model choice and vendor-lock-in positioning reference. |
| AI Website Cloner Template | https://github.com/JCodesMore/ai-website-cloner-template | `[SKILLS][DESIGN]` | Reference | Skills library | Task-specific workflow template example. |
| Hermes IDE | https://github.com/hermes-hq/hermes-ide | `[DESKTOP][UX][ORCH]` | Reference | Agentic Desktop UX | Multi-session IDE/timeline/git/context UX inspiration. |
| Qwen3.5 Claude 4.6 Opus Distilled | https://huggingface.co/Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled | `[MODEL][RESEARCH]` | Evaluate later | Future | Community model reference; verify license/quality before use. |
| GLM-5 | https://huggingface.co/zai-org/GLM-5 | `[MODEL][REASONING]` | Evaluate later | Future | Large local/enterprise model candidate; likely heavy infrastructure. |
| manus-evolve | https://github.com/evolving-machines-lab/manus-evolve | `[RESEARCH][ORCH]` | Reference | Future | Agent evolution research/reference. |
| Aperant | https://github.com/AndyMik90/Aperant | `[BOARD][DESKTOP][SANDBOX]` | Reference | Agentic Work Board | Kanban + multi-session agent/worktree/QA loop inspiration. |
| Ollama qwen3-coder-next | https://ollama.com/library/qwen3-coder-next | `[MODEL][LOCAL][CODING]` | Candidate runtime | Agent phase | Easy local coding model runtime for dev/testing. |
| Qwen3-Coder-Next | https://huggingface.co/Qwen/Qwen3-Coder-Next | `[MODEL][LOCAL][CODING]` | Candidate model | Agent phase | Open coding model candidate behind model gateway. |
| TalkCody | https://github.com/talkcody/talkcody | `[DESKTOP][LOCAL][AGENT]` | Reference | Agent phase | Local-first coding agent reference. |
| Kilo Code | https://github.com/Kilo-Org/kilocode | `[DESKTOP][TERMINAL][GUI]` | Adapter/reference | Agent phase | Agentic engineering platform UX and modes reference. |
| Kimi K2.5 | https://huggingface.co/moonshotai/Kimi-K2.5 | `[MODEL][FRONTIER-LIKE]` | Evaluate later | Future | High-end model option; heavy infrastructure. |
| GLM-4.5 | https://github.com/zai-org/GLM-4.5 | `[MODEL][REASONING][CODING]` | Evaluate later | Future | Local/enterprise model option. |
| Stable-DiffCoder-8B-Instruct | https://huggingface.co/ByteDance-Seed/Stable-DiffCoder-8B-Instruct | `[MODEL][RESEARCH][CODING]` | Evaluate later | Future | Research model; not default serving path. |
| goose | https://github.com/aaif-goose/goose | `[DESKTOP][SKILLS][MODEL]` | Adapter candidate | Agent phase | Extensible local/open-source agent runtime. |
| goose docs | https://goose-docs.ai/ | `[DESKTOP][SKILLS][MODEL]` | Reference | Agent phase | Docs for goose agent runtime. |
| Archon | https://github.com/coleam00/Archon | `[ORCH][APPROVAL][WORKFLOW]` | Reference | Agent phase | Deterministic workflow, validation and approval gate inspiration. |
| React Native Vibe Code SDK | https://github.com/react-native-vibe-code/react-native-vibe-code-sdk | `[MOBILE][SKILLS]` | Reference | Future | Mobile-generation workflow reference. |
| Cline | https://github.com/cline/cline | `[DESKTOP][SECURITY][TERMINAL][GUI]` | Adapter/reference | Agent phase | Approval-first tool execution UX reference. |
| Playwright MCP | https://github.com/microsoft/playwright-mcp | `[GUI][BROWSER][MCP][TESTING]` | Tool candidate | Agent phase | Browser automation via MCP; good for QA and browser tasks. |
| token-optimizer-mcp | https://github.com/ooples/token-optimizer-mcp | `[CONTEXT][MODEL][COST]` | Reference | Agent phase | Token/cost optimization reference. |
| Agentic coding token paper | https://arxiv.org/abs/2604.22750 | `[COST][OBSERVE][RESEARCH]` | Reference | Agent phase | Use for token budget, cache/context policy rationale. |
| claude-video-vision | https://github.com/jordanrendric/claude-video-vision | `[GUI][EVIDENCE][VIDEO]` | Reference | Future | Video/screen recording analysis and evidence idea. |
| Stash | https://github.com/alash3al/stash | `[MEMORY][CONTEXT][MCP]` | Reference | Agent phase | Persistent agent memory reference. |
| WUPHF | https://github.com/nex-crm/wuphf | `[ORCH][CONTEXT][TEAM]` | Reference | Future | Collaborative AI employee/knowledge-base reference. |
| claude-context | https://github.com/zilliztech/claude-context | `[CONTEXT][RAG][CODE]` | Reference | Agent phase | Code search/context grounding reference. |
| cco | https://github.com/nikvdp/cco | `[SECURITY][WRAPPER]` | Reference | Agent phase | Protective wrapper idea for Claude Code-style agents. |
| design.md | https://github.com/google-labs-code/design.md | `[DESIGN][SKILLS]` | Reference | Agent-generated UI | Machine-readable design identity for agents. |
| awesome-agent-skills | https://github.com/VoltAgent/awesome-agent-skills | `[SKILLS][SECURITY]` | Reference only | Future | Skill discovery; must be reviewed before production use. |
| mattpocock skills | https://github.com/mattpocock/skills | `[SKILLS][QUALITY]` | Reference | Future | Practical engineering skill examples. |
| frontend-slides | https://github.com/zarazhangrui/frontend-slides | `[SKILLS][DESIGN][EVIDENCE]` | Reference | Future | Progressive-disclosure skill design example. |
| codebase-to-course | https://github.com/zarazhangrui/codebase-to-course | `[DOCS][SKILLS]` | Reference | Knowledge base | Codebase education/onboarding artifact generation. |
| openskills | https://github.com/numman-ali/openskills | `[SKILLS]` | Reference | Future | Universal skills loader idea. |
| andrej-karpathy-skills | https://github.com/forrestchang/andrej-karpathy-skills | `[SKILLS][QUALITY]` | Reference | Future | Coding behavior rules/pitfall guidance. |
| awesome-design-md | https://github.com/VoltAgent/awesome-design-md | `[DESIGN]` | Reference | Future | Design.md examples. |
| Trellis | https://github.com/mindfold-ai/Trellis | `[WORKFLOW][BOARD][MEMORY]` | Reference | Agentic Work Board | Specs/tasks/journals/workflows/worktrees inspiration. |
| Vercel agent-skills | https://github.com/vercel-labs/agent-skills | `[SKILLS]` | Reference | Future | Skill packaging reference. |
| Harness | https://github.com/revfactory/harness | `[SKILLS][ORCH][TEAM]` | Reference | Future | Meta-skill for designing agent teams. |
| awesome-cursorrules | https://github.com/PatrickJS/awesome-cursorrules | `[SKILLS][QUALITY]` | Reference | Future | Rule-library inspiration for project-specific agent behavior. |
| gstack | https://github.com/garrytan/gstack | `[TEAM][SKILLS]` | Reference | Future | Specialized agent/team role inspiration. |
| VoltAgent org | https://github.com/VoltAgent | `[ORCH][SKILLS]` | Reference | Future | Organization-level skill/framework repository index. |
| agency-agents | https://github.com/msitarzewski/agency-agents | `[TEAM][SKILLS]` | Reference | Future | AI agency roles and deliverables. |
| claude-skills | https://github.com/jezweb/claude-skills | `[SKILLS]` | Reference | Future | Full-stack and integration skill examples. |
| OpenAI Symphony | https://github.com/openai/symphony | `[BOARD][ORCH][EVIDENCE][SANDBOX]` | Primary reference | Agentic Work Board | Issue tracker as agent control plane; isolated workspaces and continuous runs. |
| Plane | https://github.com/makeplane/plane | `[BOARD][DOCS][SELFHOST]` | Reference/optional integration | Board roadmap | Open-source Jira/Linear-style project management. |
| Cua | https://github.com/trycua/cua | `[GUI][SANDBOX][DESKTOP][EVIDENCE]` | Future candidate | Computer-use phase | Open-source computer-use agent desktop/sandbox infrastructure. |
| Matchlock | https://github.com/jingkaihe/matchlock | `[SANDBOX][SECURITY][SECRETS]` | Future candidate | Secure agent phase | Ephemeral microVM sandbox with network allowlist and secret proxying. |
| VoltAgent | https://github.com/voltagent/voltagent/ | `[ORCH][OBSERVE][SKILLS]` | Reference/candidate | Agent phase | TypeScript agent framework with memory/RAG/guardrails/tools/MCP/workflows and ops console. |
| Open Notebook | https://github.com/lfnovo/open-notebook | `[CONTEXT][DOCS][MODEL]` | Reference | Knowledge base | Self-hosted NotebookLM-like knowledge workspace with model choice. |
| Paperclip | https://github.com/paperclipai/paperclip | `[BOARD][ORCH][GOVERNANCE]` | Long-term inspiration | Future | AI organization/governance/goal alignment inspiration. |
| Grass Expo | https://github.com/anildukkipatty/grass-expo | `[MOBILE][QR][APPROVAL]` | Reference | Agentic Desktop mobile | Mobile chat/review/approval UX inspiration. |
| Grass IDE | https://github.com/anildukkipatty/grass-ide | `[MOBILE][QR][REMOTE]` | Reference | Agentic Desktop mobile | Run command, scan QR, control coding agent from phone. |
| Project Nomad | https://github.com/Crosstalk-Solutions/project-nomad | `[OFFLINE][DOCS][MODEL]` | Reference | Offline packaging | Offline/field bundle mindset. |
| Tolaria | https://github.com/refactoringhq/tolaria | `[DOCS][CONTEXT]` | Reference | Knowledge base | Markdown knowledge-base desktop app pattern. |
| Potpie | https://github.com/potpie-ai/potpie | `[CONTEXT][CODE][AGENT]` | Reference | Context service | Codebase knowledge graph / code intelligence for agent context. |
| Atlassian Jira Agile docs | https://confluence.atlassian.com/agile064 | `[BOARD][AGILE][SCRUM][KANBAN]` | Reference | Future Engineering OS | Agile/Scrum/Kanban board patterns. |
| Atlassian Confluence knowledge base | https://www.atlassian.com/software/confluence/resources/guides/best-practices/knowledge-base | `[DOCS][KNOWLEDGE]` | Reference | Future Engineering OS | Knowledge-base spaces, templates, permissions, articles. |
| Gitea | https://docs.gitea.com/ | `[CODE][FORGE][SELFHOST]` | Candidate integration | Future code forge | Self-hosted Git hosting, code review, packages, CI/CD. |
| GitLab CI/CD | https://docs.gitlab.com/ci/ | `[CODE][CICD][DEVSECOPS]` | Candidate integration | Future code forge | CI/CD and DevSecOps reference. |
| Keycloak | https://www.keycloak.org/ | `[IDENTITY][SSO][SECURITY]` | Candidate dependency | MVP | Open-source identity and access management. |
| Authentik | https://docs.goauthentik.io/ | `[IDENTITY][SSO][SECURITY]` | Candidate dependency | MVP | Self-hosted identity provider supporting OIDC/SAML/LDAP/SCIM. |
| Claude Code Remote Control | https://code.claude.com/docs/en/remote-control | `[MOBILE][QR][REMOTE][AGENT]` | Product reference | Agentic Desktop mobile | QR/session URL mobile continuation pattern; adapt as short-lived pairing not credential transfer. |
| LiteLLM Proxy | https://docs.litellm.ai/docs/simple_proxy | `[MODEL][GATEWAY]` | Candidate dependency | Agent phase | Self-hosted/OpenAI-compatible model gateway. |
| vLLM OpenAI-compatible server | https://docs.vllm.ai/en/v0.8.3/serving/openai_compatible_server.html | `[MODEL][LOCAL][SERVER]` | Candidate dependency | Agent phase | Production local model serving with OpenAI-compatible API. |
| Ollama | https://ollama.com/ | `[MODEL][LOCAL][DEV]` | Candidate dependency | Agent phase | Simple local model runtime for development/testing. |
