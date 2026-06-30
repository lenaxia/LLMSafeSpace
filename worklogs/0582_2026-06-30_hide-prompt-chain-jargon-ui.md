# Worklog: Hide prompt-chain jargon in org and workspace UIs

**Date:** 2026-06-30
**Session:** Fix issue #480 — the org-admin Agent Config tab and the workspace user's Custom Instructions drawer leaked internal prompt-chain implementation terminology ("Overlay", "platform prompt", "Appended", "role's system prompt") into UX copy that non-engineering audiences see.

**Status:** Complete

---

## Objective

End users and org admins should not see architectural concepts (the multi-layer prompt chain) in their UX copy. The platform-admin tab legitimately needs the chain language because that audience owns the layering — keep it. Everywhere else: rewrite to neutral, audience-appropriate copy without implementation jargon.

---

## Work Completed

### Investigation

- Grepped `frontend/src` for `[Aa]ppended`, "platform prompt", "role's system prompt", "Org Prompt Overlay" and related leakage terms.
- Identified two affected components: `OrgAgentConfigTab.tsx` (org admin) and `WorkspaceSettingsDrawer.tsx` (workspace user).
- Confirmed platform-admin scope decision with maintainer: keep `PlatformAgentConfigTab.tsx` chain language because that audience owns the chain.
- Decided what to keep vs rewrite in the org-admin tab: the agent-roles editor section uses "role" as a legitimate feature name; only the prompt-toggle and prompt-overlay cards needed jargon scrubbing.

### Validated assumptions

1. **No test asserts the old strings.** Validated by grep — no `OrgAgentConfigTab.test.tsx` existed (significant prior gap I filled in this PR), and `WorkspaceSettingsDrawer.test.tsx` did not assert the helper-text wording.
2. **`Dialog.Overlay` is a Radix UI primitive, not user-visible text.** Confirmed — the only remaining "Overlay" match in `WorkspaceSettingsDrawer.tsx` is the Radix component `<Dialog.Overlay>` for the modal backdrop. Not a copy leak.
3. **The Save button's "Save Overlay" was leaky too.** This was an adversarial-test catch — my initial card-only edit missed the button text. The `queryByText(/Overlay/) → not in document` assertion failed against the partially-fixed code and surfaced it.

### TDD cycle

1. Wrote 5 failing tests (4 in new `OrgAgentConfigTab.test.tsx`, 1 in `WorkspaceSettingsDrawer.test.tsx`) asserting:
   - The new copy renders (positive)
   - The leaky terms are absent from the rendered DOM (adversarial)
2. Ran red — 5 failures.
3. Applied copy edits in `OrgAgentConfigTab.tsx` (6 surfaces: toggle card description, disabled caption, card title, helper text, Save button, toast) and `WorkspaceSettingsDrawer.tsx` (1 surface: helper text).
4. Ran green — 5/5.
5. Full frontend suite — 1257/1257.

### Review-feedback iteration

After the AI review approved with two non-blocking notes:
- Added a 5th `OrgAgentConfigTab` test that exercises the save flow and asserts the rewritten toast renders.
- Added this worklog entry.

---

## Key Decisions

- **Scope: org-admin + workspace-user only.** Platform admins keep their chain language because the layering IS their responsibility. Confirmed with maintainer before writing code.
- **Keep "role" in the agent-roles editor.** The agent-roles editor is a separate feature whose entire purpose is managing agent roles. The word "role" appears legitimately there as the feature name. Only the prompt-toggle and prompt-overlay cards were scrubbed.
- **Keep `placeholder="Org-specific instructions..."`** in the org prompt textarea. "Org-specific" is plain descriptive English, not implementation jargon. Removing it would have been over-application of the rule.
- **Adversarial absence assertions for every changed string.** Every test asserts BOTH "new string renders" AND "old jargon term is absent". This caught the "Save Overlay" button leak that the card-only edit missed.

---

## Blockers

None.

---

## Tests Run

- `cd frontend && npx vitest run src/components/org-admin/OrgAgentConfigTab.test.tsx src/components/workspace/WorkspaceSettingsDrawer.test.tsx` — initial RED with 5 failures matching "expected new copy / found old copy"; GREEN after copy edits; final GREEN with toast test added (5/5 on the new file + 15/15 on the workspace drawer file).
- `cd frontend && npx vitest run` — full frontend suite: 1257 tests pass across 116 files. Confirms no sibling test depended on the old strings.

---

## Next Steps

- Deploy to `home-kubernetes` by bumping the frontend image tag in `talos-ops-prod/.../helm-release.yaml` once the CI run for this PR's merge commit publishes its `ts-*` tag.

---

## Files Modified

- `frontend/src/components/org-admin/OrgAgentConfigTab.tsx` — 6 user-facing strings rewritten (toggle description, disabled caption, card title, helper text, Save button, toast).
- `frontend/src/components/org-admin/OrgAgentConfigTab.test.tsx` — new file (first test for this component); 5 tests asserting copy presence + jargon absence + save-toast flow.
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx` — helper text rewritten with concrete examples.
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.test.tsx` — 1 new test asserting new helper text renders, old jargon absent.
- `worklogs/0582_2026-06-30_hide-prompt-chain-jargon-ui.md` (this file).
