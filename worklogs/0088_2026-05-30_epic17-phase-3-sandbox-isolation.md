# 0088 — Epic 17 Phase 3 — Sandbox Isolation & Container Escape

**Date:** 2026-05-30
**Author:** mikekao + opencode (sonnet)
**Epic:** 17 — Security Review & Penetration Test
**Phase:** 3 — Sandbox Isolation & Container Escape
**Cluster:** `admin@home-kubernetes` (Talos, post-fix image `sha-eb5c33e`)

---

## Summary

Ran 17 RT-3.x tests against live sandbox pods. **15 executed**, 2 quarantined (RT-3.11 fork-bomb, RT-3.15 node-disk forensics — both have node-level blast-radius). Result: **9 PASS, 6 FAIL, 0 INCONCLUSIVE, 2 SKIP**.

Surfaced **4 new gaps** (G21-G24):
- **G21** medium — `/sandbox-cfg/password` mode 0644 (init-script `cp` preserves 0644 from Secret mount)
- **G22** low — `enableServiceLinks: true` leaks namespace topology to sandbox via env vars
- **G23** medium — `/workspace` PVC mount lacks `nosuid`
- **G24** low — No `seccompProfile` on workspace pod (cap-drop+NoNewPrivs already EPERM dangerous syscalls today)

Confirmed **2 prior gaps still hold**: G16 (NetPol), G17 (SA token), via independent re-tests from inside fresh sandboxes.

Confirmed **G1 still exploitable**: `/tmp` is writable+exec on the live cluster (cp /bin/sh /tmp/x; /tmp/x → "PWNED").

---

## Methodology — assumption-first

Per the user's standing instruction: state every assumption up front, validate before any test execution, never advance on unvalidated hypotheses.

### A — cluster & deployment state assumptions
| # | Assumption | Validation | Result |
|---|---|---|---|
| A1 | post-fix image `sha-eb5c33e` deployed | `kubectl get pods` image fields | ✅ all 4 LLMSafeSpace pods on sha-eb5c33e |
| A2 | `networkPolicy.enabled: true` | `helm get values --all` | ✅ `enabled: true`, `blockedEgressCIDRs` includes 169.254.0.0/16 |
| A3 | 2 NetworkPolicies live | `kubectl get netpol -n default` | ✅ `llmsafespace-workspace-default-deny-ingress`, `llmsafespace-workspace-egress` |
| A4 | G17 fix on pod spec (`automountServiceAccountToken: false`) | jsonpath on live sandbox pod | ✅ confirmed `false` |
| A5 | `/tmp` is `emptyDir{}` (G1 status quo) | jsonpath on `.spec.volumes` | ✅ `tmp`, `sandbox-cfg`, `sandbox-home` all plain emptyDir, no `medium: Memory` |
| A6 | sandbox runs as `runAsUser: 1001` (per plan) | jsonpath on `.spec.securityContext` | ❌ **REFUTED** — actual UID is **1000**. Plan was outdated. Tests adjusted. |
| A7 | no seccompProfile attached | jsonpath on pod and container securityContext | ✅ confirmed absent |

A6 was the only refuted assumption. Plan reviewed; UID 1000 is correct (matches `controller.go` and `runAsGroup`).

### B — methodology assumptions
| # | Assumption | Validation | Result |
|---|---|---|---|
| B1 | Phase 2 harness pattern is reusable | Read `phase-2/harness/run-phase2.py` | ✅ copied register/login/createWorkspace patterns; deterministic password seed |
| B2 | `kubectl exec` works as the in-pod attacker | `kubectl exec ... -- id` | ✅ runs as uid=1000(sandbox) |
| B3 | Sandbox pods stay running long enough for multi-step tests | provisioning watch | ✅ pods reach Active in ~30s, stay alive while harness runs |
| B4 | Default `runtime: base` is the right surface | provisioning a `base` workspace | ✅ pod boots correctly |

### C — blast-radius classification
🟢 safe-on-live: RT-3.1, 3.2, 3.3, 3.4, 3.6, 3.8, 3.9, 3.10, 3.12, 3.13, 3.14, 3.16
🟡 caution: RT-3.5, 3.7, 3.17 (touch PVC or kernel-adjacent surfaces but reversible)
🔴 quarantine: RT-3.11 (resource exhaustion → node OOM risk), RT-3.15 (node-shell required)

### D — assumptions deliberately NOT made
- I did not assume Cilium FQDN policies are in use (they aren't — verified via `kubectl get netpol -o yaml`).
- I did not assume seccomp default profile is applied (it isn't — verified via pod spec).
- I did not assume kernel-namespace isolation defeats `unshare` without seccomp (verified by direct test — it does, via cap-drop).
- I did not assume the `python3` interpreter is in the base image (it isn't — discovered, then rewrote 5 tests to use bash `/dev/tcp` and perl).

---

## Tooling discoveries inside the live sandbox

```
SHELL=/bin/sh; bash available; perl available
NO python3, NO nc, NO wget, NO dig, NO nslookup
HAVE curl, getent, perl
Kernel: 6.18.18-talos x86_64
```

Adjustments made:
- TCP probes: `timeout 3 bash -c '</dev/tcp/IP/PORT'` (rc 0 = connected, non-zero = blocked)
- DNS probes: `getent hosts <name>` (handles IPv6 too — original test broke on cluster's v6-first behaviour)
- Raw socket / port-bind: `perl -MIO::Socket::INET ...`
- Syscall probes: `perl -e 'syscall(N, ...)'`

---

## Per-test results

| ID | Result | Severity | Notes |
|---|---|---|---|
| RT-3.1 | PASS | info | K8s API DNS resolves but TCP blocked — G16 NetPol holds |
| RT-3.2 | PASS | info | `/var/run/secrets/kubernetes.io/` absent + spec field `false` — G17 holds |
| RT-3.3 | FAIL | low | service-link env leaks namespace topology (NEW G22) |
| RT-3.4 | FAIL | high | `/tmp` is rw+exec — G1 confirmed |
| RT-3.5 | FAIL | medium | `/workspace` ext4 mount lacks `nosuid` (NEW G23) |
| RT-3.6 | PASS | info | CapEff=0, CapBnd=0, NoNewPrivs=1; chown/mknod/raw-socket all EPERM |
| RT-3.7 | FAIL | low | no seccompProfile, but cap-drop+NoNewPrivs already EPERM dangerous syscalls (NEW G24) |
| RT-3.8 | PASS | info | 169.254.169.254 unreachable (Talos node FW + G16 NetPol both block) |
| RT-3.9 | PASS | info | alice → bob's pod IP:8080 connect blocked — G16 holds |
| RT-3.10 | PASS | info | external DNS works (accepted exfil risk; NetPol egress allows 0.0.0.0/0 minus RFC1918) |
| RT-3.11 | SKIP | — | quarantined (node OOM blast-radius) |
| RT-3.12 | PASS | info | PID-ns isolated; `kill -0 99999` ESRCH |
| RT-3.13 | PASS | info | `/etc/shadow` not readable from sandbox |
| RT-3.14 | PASS | info | /dev/kmsg, /dev/mem, /dev/sda absent or unopenable |
| RT-3.15 | SKIP | — | quarantined (node-shell required) |
| RT-3.16 | FAIL | medium | `/sandbox-cfg/password` mode 0644 (NEW G21) |
| RT-3.17 | FAIL | medium | mise install path writable; persists across pod restart (G19-adjacent, accepted-by-design) |

---

## Forensic deep-dive on each FAIL

For every FAIL, I `kubectl exec`'d directly into the live sandbox to verify the harness-reported behaviour matches reality. Two harness false-positives were caught and fixed:

1. **RT-3.6 false-positive**: harness flagged `bind-to-port-80 succeeded → CAP_NET_BIND_SERVICE present`. Direct check: `CapEff=0`, `NoNewPrivs=1` — caps are dropped. Root cause: `net.ipv4.ip_unprivileged_port_start = 0` (a sysctl, not a capability). Updated test to (a) read `/proc/self/status` for the authoritative cap mask, (b) note the unprivileged-port behaviour in the PASS rationale.
2. **RT-3.3 false-positive**: harness flagged `OPENCODE_SERVER_PASSWORD` as a leaked credential. Threat model says: this password authenticates the in-pod opencode HTTP server; the attacker is already in the pod and can hit opencode on localhost; same-trust = not a finding. Updated test to maintain a `SAME_TRUST` allowlist and re-classify hits accordingly. Re-run found a real residual: 30+ service-link env vars (G22).

This is the mutation-validation discipline applied to harness logic itself — every test must produce a reproducible determination from observable state.

---

## New finding details

### G21 — /sandbox-cfg/password mode 0644

**Location:** `controller/internal/workspace/controller.go:733-738`
```go
credScript := `
if [ -f /mnt/secrets/user-secrets/secrets.json ]; then
  cp /mnt/secrets/user-secrets/secrets.json /sandbox-cfg/secrets.json
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`
```

**Reproduction:** `ls -l /sandbox-cfg/password` → `-rw-r--r--`. Source mode (Secret defaultMode 420 = 0644 octal) is preserved by `cp`.

**Distinct from G20:** G20 was about `pkg/agentd/secrets` writing `/tmp/agent-config.json` atomically with mode 0600. The G20 fix did NOT touch the init-container's bash `cp` script.

**Fix:**
```sh
install -m 0600 /mnt/secrets/password/password /sandbox-cfg/password
install -m 0600 /mnt/secrets/user-secrets/secrets.json /sandbox-cfg/secrets.json   # in the conditional branch
```

### G22 — enableServiceLinks: true (recon leak)

**Location:** `controller/internal/workspace/controller.go` — pod spec construction never sets `EnableServiceLinks`, so K8s defaults it to `true`.

**Reproduction:** 30+ env vars like `LLMSAFESPACE_API_SERVICE_HOST=10.96.44.192` in PID-1 environ.

**Fix:** Add `EnableServiceLinks: ptr.To(false)` to the pod spec.

### G23 — /workspace PVC mount lacks nosuid

**Location:** Helm chart's storage class definition for workspace PVCs (currently relies on Longhorn default).

**Reproduction:** `grep ' /workspace ' /proc/mounts` → `/dev/longhorn/pvc-... /workspace ext4 rw,seclabel,relatime` (no nosuid, no nodev).

**Fix:** Add Helm value `storage.workspace.mountOptions: ["nosuid","nodev"]` and apply via the Longhorn StorageClass.

### G24 — No seccomp profile

**Location:** `controller/internal/workspace/controller.go` — pod-level `SecurityContext` lacks `SeccompProfile`.

**Mitigation context:** Cap-drop ALL + NoNewPrivs:1 already EPERM the dangerous syscalls I probed (`unshare`, `clone`, `ptrace`, `add_key`, `request_key`). Severity is therefore **low** (defence-in-depth).

**Fix:**
```go
PodSecurityContext: &corev1.PodSecurityContext{
    ...
    SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
}
```

---

## Phase 2 cleanup gap caught

While inventorying users for Phase 3 provisioning, found **42 stale `@pentest.local` users** and **18 stale workspaces** from Phase 2 (despite worklog 0087 reporting cleanup verified). These are leftover from harness runs that didn't complete cleanup paths (likely Ctrl-C exits or test failures).

**Action:** Cleaned manually; will keep the hygiene check at the start of every future Phase. Recommend adding a Phase 0 "pre-flight" idempotent cleanup that nukes any stray `@pentest.local` rows before each phase starts.

---

## Cleanup performed

| Item | Action |
|------|--------|
| `phase3-alice@pentest.local`, `phase3-bob@pentest.local` users | DELETE FROM users (FK CASCADE wipes workspaces row) |
| Their workspace CRs | `kubectl delete workspace` (named explicitly) |
| Smoketest user `phase3-smoketest-only@pentest.local` and its workspace | deleted during dev |
| Phase-2 residue (42 users, 18 ws) | bulk DELETE via SQL |
| Port-forward 19090 | left running for next phase |

---

## Files added / changed

- `design/stories/epic-17-security-review/phase-3/harness/run-phase3.py` — 1077-line Python harness, 17 test functions, structured findings JSON, deterministic-password fixtures, blast-radius gating
- `design/stories/epic-17-security-review/phase-3/findings.md` — consolidated report
- `design/stories/epic-17-security-review/phase-3/evidence/RT-3.{1..17}.json` — per-test structured evidence
- `worklogs/0088_2026-05-30_epic17-phase-3-sandbox-isolation.md` — this file
- (deferred) `design/stories/epic-17-security-review/THREAT-MODEL.md` — to update after all phases done, with G21-G24 added en bloc

---

## Next: Phase 4 — Credential & Crypto Testing

Tests RT-4.1 through RT-4.7. Mostly API-level — credential IDOR, log redaction, JWT exp/sig, key wrapping. Will reuse harness skeleton from Phase 2/3.
