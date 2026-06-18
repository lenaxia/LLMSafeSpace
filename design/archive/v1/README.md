# V1 Design Docs (Archived)

**Status:** Superseded by V2
**Archived:** 2026-06-18 (US-46.14)
**Authoritative successor:** [`../../0021_2026-05-21_evolution-v2.md`](../0021_2026-05-21_evolution-v2.md)

---

## Why these are archived

These twenty documents (`0001`–`0020`) describe the **V1 architecture** of
LLMSafeSpace: Sandbox / SandboxProfile / WarmPool / WarmPod CRDs, the original
controller design, and the V1 API surface.

V2 (`design/0021_evolution-v2.md`, v2.4) **supersedes** them wherever they
conflict. Specifically:

- The V1 Sandbox / SandboxProfile / WarmPool / WarmPod CRDs were **removed**.
  The `Workspace` CRD absorbs all sandbox and profile functionality.
- The V1 controller reconciliation design was rewritten around `Workspace`.
- The V1 runtime-environment and network-policy designs were reworked.

A reader landing directly in this directory should treat everything here as
**historical reference only**, not a specification to implement against. If a
V1 doc and `0021_evolution-v2.md` disagree, `0021` wins.

---

## Index (historical context)

| Doc | Original subject |
|-----|------------------|
| `0001_2025-03-05_architecture.md` | System overview, deployment topology, security model |
| `0002_2025-03-05_api.md` | V1 API surface |
| `0003_2025-03-05_controller.md` | Controller specification (V1 CRDs, reconciliation loops) |
| `0004_2025-03-05_warmingpool.md` | WarmPool / WarmPod (removed in V2) |
| `0005_2025-03-05_security.md` | Defense-in-depth security model |
| `0006_2025-03-05_implementation.md` | Implementation plan |
| `0007_2025-03-05_runtimeenv.md` | Runtime environments |
| `0008_2025-03-05_apiservice.md` | API service internals |
| `0009`–`0019` | Controller deep-dive series (architecture, components, CRDs, reconciliation, etc.) |
| `0020_2025-03-05_network.md` | Network policy design and egress filtering |

---

## When to read these

- Investigating why a V1-era decision was made.
- Understanding a field name or code comment that references the old Sandbox model.
- Reviewing the historical security or network model before a V2-era change.

## When NOT to read these

- Implementing a new feature against them — always read `0021_evolution-v2.md` first.
- As a CRD schema reference — use `pkg/apis/llmsafespace/v1/` instead.
