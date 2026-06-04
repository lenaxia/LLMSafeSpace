Repository: LLMSafeSpace — a Kubernetes-first platform (Go) for running AI agents securely in isolated sandboxes. Every sandbox runs `opencode serve` as a persistent HTTP server with a PVC-backed persistent workspace. Single maintainer: @lenaxia.

Key directories:
- api/               — Go API service (Gin) + MCP server; reverse proxy to sandbox agents, workspace/credential management
- controller/        — Kubernetes operator (controller-runtime); manages Sandbox, Workspace, SandboxProfile, RuntimeEnvironment CRDs
- runtimes/          — Container images (Python, Node.js, Go); hardened environments with opencode serve and credential injection
- pkg/               — Shared packages (types, kubernetes client, redact, logger, utilities)
- cmd/               — Top-level binaries (redact, mcp)
- design/            — Architecture and design documents (EVOLUTION-V2.md is authoritative for V2)
- design/SECURITY.md — Defense-in-depth security model
- .github/workflows/ — CI/CD pipelines

**Before doing anything else: read README-LLM.md at the repo root.** It contains the full architecture overview, coding standards, hard rules, and development workflow. Every response must be consistent with it.
