# US-5.3: Write Kyverno Admission Policies

**DEFERRED to V2.1** — Security defense-in-depth. Pod security contexts in the controller already enforce the basics (readOnlyRootFilesystem, runAsNonRoot, drop ALL capabilities). Kyverno adds admission-level enforcement for operators who want extra guarantees.

**Epic:** 5 - Security Hardening
**Priority:** Medium

## User Story

As a platform operator, I want Kyverno policies enforcing security constraints at the K8s admission level, so that even if the controller has a bug, insecure pods cannot be created.

## Acceptance Criteria

- [ ] Policy: require read-only root filesystem
- [ ] Policy: require non-root user
- [ ] Policy: deny Secret env var refs on main containers (credential-setup exempted)
- [ ] Policies applied only to pods with `app: llmsafespace` label
- [ ] failureAction: Enforce

## Technical Details

**New file:** `charts/llmsafespace/templates/kyverno/enforce-sandbox-pod-security.yaml`

See design §9.6 for the full policy YAML.

Note: OPENCODE_SERVER_PASSWORD is NOT set via secretKeyRef — the entrypoint reads it from a file at runtime. This satisfies the policy.

## Design Reference

Section 9.6: Kyverno Admission Policies

## Effort

Medium (2-3 hours)
