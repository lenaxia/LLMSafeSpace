# Worklog: Add Rule 12 — Containment Before Abstraction

**Date:** 2026-06-29
**Session:** Decide when to abstract the opencode agent dependency, then codify the decision as a new hard rule in `README-LLM.md`
**Status:** Complete

---

## Objective

Answer a strategic question and make it durable: should this project stay tied to `opencode serve`, build its own agentic harness, or abstract the agent behind a provider interface — *and when*? The outcome needed to be written down as a project rule so future sessions apply it consistently instead of re-deriving it.

---

## Work Completed

### Strategic decision (recorded in PR #446 description)

Concluded the platform layer (orchestration, isolation, multi-tenant control) is the product — not the agent loop. The coupling to opencode is mostly *accidental* (config-merge semantics, provider-ID model, the relay-config subsystem) rather than *essential* (chat+tools transport, credential injection, health). Building a homegrown harness was rejected: it is a different business than running agents securely for tenants, against well-funded incumbents, and would dilute the actual moat. Staying tightly coupled was also rejected. The answer is: **contain now, abstract when a forcing event arrives.**

### Rule 12 — Containment Before Abstraction (External-Dependency Coupling)

Added to `README-LLM.md` at the end of the Critical Guidelines & Hard Rules section (as Rule 12, the next number after 0–11). Three states codified:

1. **Containment now (cheap, mandatory)** — keep external-dependency knowledge behind a single seam (a folder/package boundary, not an interface). Platform code must not learn that the agent is opencode. Containment is not abstraction: no interface design, no generics, no provider registry.
2. **Do NOT abstract prematurely** — a single consumer tells you nothing about interface shape; designing one now would freeze opencode's current behaviour (the relay-config subsystem) into a contract we'd then have to break. This is the speculative-abstraction tax Rule 4 already prohibits.
3. **Trigger the real abstraction** when one of: (a) a second consumer is funded (e.g. Claude Code), (b) a forcing rewrite occurs, or (c) pain recurs in the same seam (containment has failed).

Scope is general (agents, cloud drivers, relay VM binaries, MCP SDKs); the opencode agent provider is the primary case because the coupling is deepest there.

### Review iterations

- **Round 1** (commit `17220c45`): AI reviewer APPROVED with two minor findings — (1) "agent-config.json writers" (plural) imprecise since the subsystem is single-writer post-US-46.10; (2) `Version` / `Last Updated` metadata not bumped despite a substantive doc change.
- **Round 2** (commit `1735eebb`): fixed both — "writers" → "write architecture"; bumped 1.20 → 1.21 and 2026-06-23 → 2026-06-29. Reviewer re-verified all claims against the source document and APPROVED again.

---

## Key Decisions

1. **Don't fork opencode, don't build a harness, abstract later** — the platform is the moat; the agent loop is commoditising. Owning the loop is a different business than running agents securely for tenants, against well-funded incumbents, and would dilute the actual moat. The only defensible posture is being the best place to *run* an agent securely for many tenants. (Recorded in PR #446 description, not in the rule itself — the rule is about *when to abstract*, not the build-vs-buy verdict.)

2. **Containment, not abstraction, as the current action** — Rule 4 already prohibits speculative abstraction. Rule 12 makes that concrete for the opencode coupling: a boundary now, an interface only when a forcing event arrives. The relay-config subsystem is called out as the canonical accidental-coupling example so future sessions recognise the pattern.

3. **Three explicit abstraction triggers** — a second funded consumer (the strongest signal), a forcing rewrite (sunk cost makes it cheap), or recurring pain in the same seam (containment has failed). Single inconvenience is explicitly *not* a trigger.

4. **Rule scope is general** — written for any external dependency whose internals we accommodate, not just the agent. Cloud drivers, relay VM binaries, and MCP SDKs are named so the rule applies when the next coupling appears.

---

## Assumptions and Validation

Per Rule 7 — assumptions stated and validated:

1. **Rules are numbered 0–11; 12 is the next number.** Validated: reviewer independently confirmed in both rounds ("rules run 0–11; 12 is correct").
2. **The Critical Guidelines section is not in the top-of-file TOC** (which lists only section-level entries), so adding a rule needs no TOC update. Validated: reviewer confirmed TOC unchanged.
3. **The relay-config claims in Rule 12 are accurate** (last-writer-wins, `OPENCODE_CONFIG` always wins, no hot reload, one-shot injector, 20s stale window). Validated: reviewer traced each to lines 482–487 / 516 / 564 of the same document.
4. **The Rule 4 cross-reference is accurate** (Rule 4 prohibits speculative abstractions). Validated: reviewer confirmed at line 135.
5. **The subsystem is single-writer post-US-46.10**, so the plural "writers" was imprecise. Validated: README lines 493–508 document the single `AgentConfigWriter`; corrected to "write architecture" in round 2.

---

## Blockers

None.

---

## Tests Run

N/A — docs-only change to `README-LLM.md`. No code, config, CRD, or behaviour change. No test levels apply. CI verification (all green on PR #446): Lint, Trivy, govulncheck, Gitleaks, pkg/secrets integration, frontend build (amd64 + arm64), AI review (APPROVE ×2).

---

## Next Steps

1. Merge PR #446 (squash) once the remaining docs-irrelevant checks finish.
2. Begin opportunistic containment per the new rule: when next touching an opencode-specific area of the platform code, peel a local boundary so external-dependency knowledge stops bleeding into services/handlers. No dedicated refactor project yet — wait for a forcing event (a second agent consumer, an opencode breaking change, or recurring pain in the relay-config seam) before paying for a real agent-provider interface.

---

## Files Modified

- `README-LLM.md` — added Rule 12 (Containment Before Abstraction); bumped Version 1.20 → 1.21 and Last Updated 2026-06-23 → 2026-06-29.
- `worklogs/0567_2026-06-29_containment-before-abstraction-rule.md` — this worklog (new).
