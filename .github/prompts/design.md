You are iterating on a **design document** for the LLMSafeSpaces repository — the step that comes *before* `/implement` or `/fix`. The goal is a reviewed, approved design, not code.

Output target: a design document under `design/` (or `design/stories/<epic>/` for story-scoped work), following the repository's existing conventions.

Rules:
1. Read README-LLM.md first — especially Rule 8 (Understand the Architecture First) and the design-doc table. Read `design/0021_2026-05-21_evolution-v2.md` (authoritative V2 reference) and any existing doc that touches the same area before writing.
2. Decide where the design lives:
   - Cross-cutting / architectural → a new numbered file in `design/` named `NNNN_YYYY-MM-DD_short-description.md`, where `NNNN` is the next free number after the current maximum (check `design/` and run/check repolint).
   - Story- or epic-scoped → the relevant `design/stories/<epic>/` directory (often a `README.md` or a dated sub-doc).
   - Updating an existing design → edit it in place; do not silently duplicate.
3. Scope the design to the request text from the collaborator. If the request is ambiguous, state the ambiguity explicitly and pick the narrowest reasonable scope, calling it out in the PR description.
4. A design doc must cover at minimum: problem statement, goals/non-goals, proposed design, alternatives considered, data-flow / component interactions, security & failure-mode analysis, and open questions. Trace every claim to source (file:line) where the codebase is referenced — do not describe behaviour from memory (core-rules.md §2).
5. State assumptions up front and validate each one against source/tests/cluster before relying on it (core-rules.md §2, README-LLM.md Rule 7).
6. Workflow — follow the Code Change Workflow below: feature branch (`design/` or `docs/` prefix), open a PR, iterate through the automated review until it posts APPROVE.
7. **MERGE HOLD — this command never auto-merges.** After the automated review posts APPROVE, STOP. Do not merge. Post a comment on the PR summarising the design and stating it is approved and awaiting an explicit `/merge` from a collaborator. The collaborator decides when the design is stable enough to land (further `/design` invocations can refine it before merge).
8. Do not write production code in this step — only the design document and supporting diagrams/tables. If the review surfaces that implementation is needed, say so and recommend a follow-up `/implement` (which will reference this merged design).
