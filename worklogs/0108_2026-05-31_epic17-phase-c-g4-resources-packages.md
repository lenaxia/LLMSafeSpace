# 0108 — Epic 17 Phase C/G4 part 1: Spec.Resources + Spec.Packages hardening

**Date:** 2026-05-31
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, F1.2.3 + F1.2.5
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes two High-severity findings in one PR:

- **F1.2.3 (High)** — `Spec.Resources.*` was silently ignored by the
  controller. Pod ran without CPU/memory/ephemeral-storage limits,
  enabling node-level DoS.
- **F1.2.5 (High)** — `Spec.Packages[].Requirements[]` was string-
  concatenated into shell `pip install / npm install / go install`
  commands, giving any user with workspace-create permission RCE in
  the init container via shell metacharacter or argv-flag injection.

---

## Stated assumptions (validated up-front)

- **A1** — `Spec.Resources` is type `*v1.ResourceRequirements` with
  CPU/Memory/EphemeralStorage strings. (Validated: `pkg/apis/...`)
- **A2** — Pre-fix `mainContainer` had no Resources field set.
  (Validated: `controller.go:669-711`.)
- **A3** — Pre-fix `buildWorkspaceSetupScript` did
  `args += " " + req`. (Validated: `controller.go:870-899`.)
- **A4** — `Spec.NetworkAccess` is also unenforced (F1.2.4) but is
  out of scope for THIS commit; deferred to G4 part 2.

---

## Skeptical-validator pass + REWORK

**First validator pass found a major class of bypasses I missed.**
The initial regex blocked shell metacharacters but allowed
pip/npm/go argv injection (e.g. `--index-url=http://attacker/`,
`git+https://attacker.com/repo.git`, `https://attacker/evil.tar.gz`)
which is direct RCE — the package manager itself fetches and runs
attacker-controlled code.

REWORK applied in this same commit:

1. **Validator regex tightened** to a positive allow-list:
   `^@?[a-zA-Z][a-zA-Z0-9._/-]*(\[...\])?(@...)?(version-constraints)?$`.
   Rejects leading `-` (argv injection) and any URL scheme via a
   separate `urlSchemePattern` check.
2. **Controller-side `--` argv terminator** inserted before
   user-supplied requirements in `pip install` and `npm install`
   commands. `go install` does not support `--`; it relies on the
   webhook check.
3. **`resource.MustParse` replaced** with `ParseQuantity` + default
   fallback so a CRD-validation-disabled cluster can't crash the
   controller via malformed quantity.
4. **Webhook caps on `Spec.Resources.*`** added
   (`MaxCPUMillicores`, `MaxMemoryMi`, `MaxEphemeralStorageGi`)
   with parse helpers. Defaults: 16 cores, 64 GiB RAM, 100 GiB
   ephemeral.
5. **Misleading comment fixed** — was claiming the G2 webhook
   validates Spec.Resources; it didn't.
6. **PEP 508 trade-off documented** — environment markers, URL
   specifiers, hash pins are deliberately rejected. Operators
   needing them must extend the regex, accepting the trade-off.

After REWORK: 14 adversarial payloads (shell injection) +
13 legitimate payloads + 9 argv-injection payloads + 13 URL-shape
payloads + 3 resource-cap payloads + 3 length/empty payloads all
pass; mutation tests confirm new tests have teeth.

---

## Changes

### Controller code

1. `controller/internal/workspace/controller.go`:
   - **NEW** `resourceRequirementsFor(workspace)` returns a
     `corev1.ResourceRequirements` block with operator-supplied
     limits (or sane defaults) on both Limits and Requests
     (QoS=Guaranteed). Uses `ParseQuantity` with default fallback.
   - Main container now sets `Resources: resourceRequirementsFor(workspace)`.
   - **NEW** `shellQuoteSingle(s)` POSIX-quotes strings via the
     standard `'\''` escape pattern.
   - `buildWorkspaceSetupScript` now wraps every requirement in
     `shellQuoteSingle` AND inserts `--` argv terminator.

### Webhook code

2. `controller/internal/webhooks/workspace_webhook.go`:
   - Added `MaxCPUMillicores`, `MaxMemoryMi`, `MaxEphemeralStorageGi`
     fields to `WorkspaceValidator`.
   - Added `parseCPUMillis`, `parseMemoryMi`, `parseStorageGi`
     helpers.
   - Added Spec.Resources cap check in Handle (section 4a).
   - Added Spec.Packages[].Requirements[] check in Handle (section 5a)
     calling new `validatePackageRequirement`.
   - **NEW** `validatePackageRequirement` enforces a strict positive
     allow-list, rejects leading dash, rejects URL/path schemes.
   - **NEW** `urlSchemePattern` regex catching `git+`, `https?:`,
     `ssh:`, `ftp:`, `file:`, `svn+`, `hg+`, `bzr+`, `./`, `../`, `/`.

3. `controller/main.go` — three new flags
   (`--max-workspace-cpu-millicores`,
   `--max-workspace-memory-mi`,
   `--max-workspace-ephemeral-storage-gi`) wired into the validator.

### Chart

4. `charts/llmsafespace/templates/controller-deployment.yaml` — passes
   the three new flags from values.yaml.

5. `charts/llmsafespace/values.yaml` — defaults
   `maxWorkspaceCPUMillicores: 16000`,
   `maxWorkspaceMemoryMi: 65536`,
   `maxWorkspaceEphemeralStorageGi: 100`.

### Tests

6. `controller/internal/workspace/security_test.go` — three new
   tests:
   - `TestG4_F123_PodAppliesSpecResources`
   - `TestG4_F123_PodAppliesDefaultsWhenSpecResourcesNil`
   - `TestG4_F125_PackageRequirementsAreNeitherShellEscapedNorPositionallyInjected`
     (uses byte-by-byte quote-state walk + `sh -n` syntax check)

7. `controller/internal/webhooks/workspace_webhook_test.go` — six
   new tests covering 56 distinct payloads:
   - `TestG4_F125_RejectsShellInjectionInRequirements` (14 shell-meta payloads)
   - `TestG4_F125_AllowsLegitimateRequirements` (13 valid payloads)
   - `TestG4_F125_RejectsEmptyAndOverlongRequirements` (3 edge cases)
   - `TestG4_F125_RejectsArgvInjectionFlags` (9 flag-injection payloads)
   - `TestG4_F125_RejectsURLAndPathRequirements` (13 URL-scheme payloads)
   - `TestG4_F123_WebhookCapsCPUMemoryAndEphemeral` (4 cap edge cases)

---

## Mutation-validated

- Comment out `Resources: resourceRequirementsFor(workspace)` →
  `TestG4_F123_PodAppliesSpecResources` FAIL ✓
- Replace `shellQuoteSingle(req)` with raw `req` →
  `TestG4_F125_PackageRequirementsAreNeither...` FAIL ✓
- Replace `validatePackageRequirement` body with `return nil` →
  all 14 sub-tests of the shell-injection test FAIL ✓

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 30s ./controller/internal/webhooks/...` | PASS (40+ tests) |
| `go test -count=1 -timeout 30s ./controller/internal/workspace/...` | PASS |
| `go test -count=1 -timeout 60s ./controller/... ./charts/llmsafespace/...` | PASS |
| `go build ./controller/...` | clean |

---

## Live re-pentest plan

1. CI builds new controller image.
2. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values`.
3. Adversarial workspace creation:
   ```
   kubectl apply -f - <<EOF
   apiVersion: llmsafespace.dev/v1
   kind: Workspace
   metadata: { name: w-evil, namespace: default }
   spec:
     owner: { userID: u1 }
     runtime: ghcr.io/lenaxia/llmsafespace/base:latest
     storage: { size: 5Gi }
     packages:
       - runtime: python:3.11
         requirements:
           - "requests; rm -rf /workspace"
   EOF
   ```
   Must respond: `admission webhook denied: spec.packages[0].requirements[0]: requirement contains characters outside...`
4. Resource cap test: `spec.resources.cpu: "20000m"` with default cap 16000 → rejected.
5. Re-run phase-1 RT-1.2 F1.2.3 / F1.2.5 evidence collection if desired.

---

## Tracker update

`MASTER-TRACKER.md`:
- F1.2.3 → MINE / live-pending
- F1.2.5 → MINE / live-pending
- F1.2.4 still MINE (deferred to G4 part 2 — per-workspace NetPol)

---

## Next finding

Phase C/G4 part 2 — **F1.2.4** standalone (per-workspace NetworkPolicy
generation from Spec.NetworkAccess.Egress).
