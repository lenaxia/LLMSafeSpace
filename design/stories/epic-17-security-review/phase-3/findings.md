# Phase 3 — Sandbox Isolation & Container Escape — Findings

**Status:** Complete (live cluster)
**Cluster:** `admin@home-kubernetes`, image `ghcr.io/lenaxia/llmsafespace/*:sha-eb5c33e`
**Harness:** [`harness/run-phase3.py`](./harness/run-phase3.py)
**Evidence:** [`evidence/RT-3.*.json`](./evidence/)
**Worklog:** [`worklogs/0088_*-epic17-phase-3-sandbox-isolation.md`](../../../../worklogs/)

## Methodology

* Two fresh `@pentest.local` sandboxes provisioned via the public API (`phase3-alice`, `phase3-bob`).
* Each test is a Python function that runs `kubectl exec` into the sandbox; the in-pod shell IS the compromise scenario.
* Every assumption was validated up-front against the live pod spec before tests ran (see worklog 0088 §Assumptions).
* Tests classified by blast radius: 🟢 safe, 🟡 caution, 🔴 quarantined.
* Results: **9 PASS, 6 FAIL, 0 INCONCLUSIVE, 2 SKIP** (the 2 SKIPs are RT-3.11/RT-3.15 — quarantined for node blast-radius).

## Summary table

| ID | Class | Result | Severity | Title |
|---|---|---|---|---|
| RT-3.1 | 🟢 | PASS | info | K8s API unreachable from sandbox |
| RT-3.2 | 🟢 | PASS | info | SA token absent (G17 fix holds) |
| RT-3.3 | 🟢 | FAIL | **low** | Service-link env leaks namespace topology to sandbox |
| RT-3.4 | 🟢 | FAIL | **high** | Sandbox /tmp is writable AND exec-allowed (G1 confirmed) |
| RT-3.5 | 🟡 | FAIL | medium | /workspace mount missing nosuid |
| RT-3.6 | 🟢 | PASS | info | All capabilities denied (CapEff=0, NoNewPrivs=1) |
| RT-3.7 | 🟡 | FAIL | low | No seccomp profile attached to sandbox pod |
| RT-3.8 | 🟢 | PASS | info | Cloud metadata IP unreachable (G16 holds; partial — see notes) |
| RT-3.9 | 🟢 | PASS | info | Cross-sandbox connectivity denied (G16 holds) |
| RT-3.10 | 🟢 | PASS | info | Public DNS resolution permitted (accepted risk; document) |
| RT-3.11 | 🔴 | SKIP | — | Resource exhaustion (node blast-radius — defer to kind) |
| RT-3.12 | 🟢 | PASS | info | PID namespace isolated |
| RT-3.13 | 🟢 | PASS | info | /etc/shadow not readable from sandbox |
| RT-3.14 | 🟢 | PASS | info | Sensitive /dev entries blocked |
| RT-3.15 | 🔴 | SKIP | — | Plaintext secrets on node disk (node-shell required) |
| RT-3.16 | 🟢 | FAIL | medium | /sandbox-cfg/password has wrong mode (644, expected 600) |
| RT-3.17 | 🟡 | FAIL | medium | Mise install path writable from sandbox |

---

## Findings (failures, ranked by severity)

### F3.4 — Writable+exec /tmp (HIGH)

**Reproduction:**
```
$ kubectl exec <sandbox> -c workspace -- /bin/sh -c 'cp /bin/sh /tmp/x; chmod +x /tmp/x; /tmp/x -c "echo PWNED"'
PWNED
```

**Mount line:**
```
/dev/sda6 /tmp xfs rw,seclabel,relatime,inode64,logbufs=8,logbsize=32k,prjquota
```

The /tmp emptyDir is mounted from node `/dev/sda6` (XFS, host root) with no `noexec` and no `nosuid`.

**Impact:** An attacker with code execution in the sandbox (i.e., the LLM or a payload from it) can drop arbitrary executables to /tmp and run them. This bypasses any read-only-rootfs hope of stopping unsigned-code execution.

**Threat model context:** The sandbox is single-tenant by design — the user's own LLM-driven workflow IS expected to write+exec from /tmp (Python pip wheels do this, Go test binaries do this, mise-installed runtimes might). So fixing this without breaking workflows is non-trivial.

**Recommended remediation (existing G1 record):**
- Option A: `emptyDir.medium: "Memory"` — gets tmpfs but still doesn't have noexec by default; would need a custom mount.
- Option B: Use a CSI ephemeral volume with explicit `mount options: ["nosuid","noexec","nodev"]` for /tmp, AND a separate writable+exec volume that workflows can opt into (`/scratch-exec`?).
- Option C: Document the trade-off and live with it for the LLM-driven workflows.

**Status:** G1 in [THREAT-MODEL.md](../THREAT-MODEL.md). Confirmed exploitable; **finding stands**.

---

### F3.5 — /workspace PVC missing nosuid (medium)

**Reproduction:**
```
$ grep '/workspace' /proc/mounts
/dev/longhorn/pvc-... /workspace ext4 rw,seclabel,relatime
```

The PVC mount lacks `nosuid`. An attacker can `chmod 4755 binary` and the SUID bit will be honoured by the kernel.

**Mitigations currently in place:**
- `runAsNonRoot: true` + `runAsUser: 1000` — there's no root-owned file in the PVC for SUID to escalate to.
- `NoNewPrivs: 1` — set-uid execve is denied even if the bit is on.
- `capabilities.drop: ALL` — even if SUID succeeded, the target process inherits no capabilities.

So the SUID bit is effectively neutered today. Risk is **defence-in-depth only**.

**Recommended remediation:**
- Storage class for `workspace-*` PVCs should set `mountOptions: ["nosuid"]`.
- Add Helm value `storage.workspace.mountOptions` (default `["nosuid","nodev"]`).

---

### F3.16 — /sandbox-cfg/password mode 0644 (medium)

**Reproduction:**
```
$ kubectl exec <sandbox> -c workspace -- ls -l /sandbox-cfg/password
-rw-r--r--. 1 sandbox sandbox 32 May 30 18:25 /sandbox-cfg/password
```

The password file has world-readable mode (`0644`). Inside a pod with single user `sandbox`, a second process started by the sandbox shell can read it.

**Root cause:** `controller/internal/workspace/controller.go:733-738` — the `credential-setup` init container runs:
```sh
cp /mnt/secrets/password/password /sandbox-cfg/password
```
`cp` preserves the source file's mode. The source is mounted from a Kubernetes Secret with `defaultMode: 420` (= 0644 octal), so the destination inherits 0644.

**Distinct from G20:** G20 was about `/tmp/agent-config.json` (fixed in `pkg/agentd/secrets`). This is a separate gap in the **init-container bash script**, which G20's Go-package fix did not touch. Call it **G21**.

**Severity assessment:**
- Same-UID exposure only (no other user in the pod).
- The "attacker" inside the sandbox can already read everything else owned by uid 1000.
- BUT: a compromised non-shell child (e.g., a malicious browser-rendered iframe escaping into `opencode`) might run with restricted environment but still uid 1000 — would still see this file.
- Defence-in-depth: medium.

**Recommended remediation:**
```sh
install -m 0600 /mnt/secrets/password/password /sandbox-cfg/password
```
Same fix for `/sandbox-cfg/secrets.json`.

Add helm chart-render test asserting the cred-setup script uses `install -m 0600`.

---

### F3.17 — Mise install path writable, persists across pod restart (medium)

**Reproduction:**
```
$ kubectl exec <sandbox> -- /bin/sh -c '
  mkdir -p /workspace/.local/share/mise/installs/_pentest
  echo tampered > /workspace/.local/share/mise/installs/_pentest/marker
  ls -l /workspace/.local/share/mise/installs/_pentest'
total 4
-rw-r--r--. 1 sandbox sandbox 9 May 30 18:28 marker
```

`/workspace/.local/share/mise/` is on the user's PVC. The sandbox uid 1000 owns it. mise resolves PVC-first when looking up runtimes. So an attacker who compromises the sandbox once, edits e.g. `/workspace/.local/share/mise/installs/python/3.13/bin/python` to add a backdoor, then idles → on next workspace resume the backdoor runs.

**Threat model context:**
- This is **by design** — the user is expected to install runtimes via mise.
- The persistence pattern is generic to any CI/dev environment with a writable cache.
- Real concern: cross-session persistence. If the sandbox is shared across restarts (which it IS for the same user), a one-shot RCE persists.

**Recommended remediation options:**
- (A) Document as accepted; users own their PVC.
- (B) Optional `mise verify` or sha256 manifest check on resume.
- (C) Per-session ephemeral PVC (breaks the "resume my work" UX).

Recommendation: document as accepted, but add an `epic-N` story for an optional integrity-check mode controlled by Helm value.

---

### F3.7 — No seccomp profile (low)

**Pod spec:**
```yaml
securityContext:
  fsGroup: 1000
  runAsGroup: 1000
  runAsUser: 1000
  # NO seccompProfile field
```

**Syscall probes (without seccomp):**
```
unshare -U: Operation not permitted
clone: -1 EPERM
ptrace: -1 EPERM
add_key: -1 EPERM
request_key: -1 EPERM
```

All probed dangerous syscalls return EPERM **today** because of `cap-drop ALL` + `NoNewPrivs:1`. Severity is therefore **low / defence-in-depth**.

**Recommended remediation:**
Add to controller pod template:
```yaml
securityContext:
  seccompProfile:
    type: RuntimeDefault
```

Most container runtimes (containerd, CRI-O) ship a sane default that blocks ~50 dangerous syscalls (including userfaultfd, pivot_root, finit_module, kexec_load). Adding it is risk-free for our workloads.

---

### F3.3 — Service-link env leaks namespace topology (low)

**Observation:**
```
LLMSAFESPACE_API_SERVICE_HOST=10.96.44.192
LLMSAFESPACE_API_SERVICE_PORT=8080
POSTGRES_PORT_5432_TCP=tcp://10.96.72.15:5432
VALKEY_PORT_6379_TCP=tcp://10.96.185.67:6379
LLMSAFESPACE_CONTROLLER_WEBHOOK_SERVICE_HOST=10.96.219.236
TINYRSVP_PORT=tcp://10.96.167.110:8080
... and ~30 more
```

Pod spec lacks `enableServiceLinks: false`. K8s defaults this to `true`, so every Service in the namespace is materialised as env vars in every sandbox pod.

**Impact:**
- Recon: attacker learns IPs+ports of postgres, valkey, the LLMSafeSpace controller webhook, the API, the frontend.
- The egress NetworkPolicy already blocks 10.0.0.0/8, so these IPs are unreachable from the sandbox today. Risk is therefore **low**.

**Important caveat:** A NetworkPolicy regression (e.g., misconfigured Helm value) would immediately weaponise this recon data into actual exploit paths.

**Recommended remediation:**
Set `EnableServiceLinks: ptr(false)` on the workspace pod template. One-line change in `controller/internal/workspace/controller.go`. Trivially testable in a chart-render unit test.

---

## Quarantined (not run on live cluster)

### RT-3.11 — Resource exhaustion (fork bomb / memory pressure / disk fill)

**Why skipped:** Fork bomb on a Talos node could OOM-kill or starve other workloads on the same node, including the postgres / valkey / API pods. Pod-level limits SHOULD contain it, but verifying the limits hold needs to be done on a sacrificial cluster.

**Static verification done:** Pod has `resources.limits` and `resources.requests` set (verified via `kubectl get pod ... -o yaml`), with cgroup-enforced cpu/memory caps.

**Action item:** Add to dedicated kind cluster pentest run (Phase 0 kit at `phase-0/`).

---

### RT-3.15 — Plaintext secrets on node disk (G15)

**Why skipped:** Requires shelling into a Talos node, which is off-scope per the user's "only default + pentest-* namespaces" rule.

**Static evidence (from pod spec):**
```yaml
volumes:
- name: sandbox-cfg
  emptyDir: {}            # NO medium: Memory
- name: tmp
  emptyDir: {}            # NO medium: Memory
```

Both volumes default to disk-backed emptyDir on the node ext4/xfs filesystem. Until the volume is reclaimed (pod deletion + kubelet GC), `/sandbox-cfg/password`, `/sandbox-cfg/secrets.json`, `/tmp/*` all live in plaintext on the node disk, readable by anyone with node root or by node-disk forensic recovery.

**Recommended remediation:**
```yaml
volumes:
- name: sandbox-cfg
  emptyDir:
    medium: Memory
    sizeLimit: 8Mi
```

(Existing G15 finding — this Phase confirms the static surface.)

---

## Pre-existing fixes confirmed live

| Gap | Fix | Phase 3 evidence |
|-----|-----|------------------|
| G16 | NetworkPolicy default-deny ingress + egress blockedCIDRs | RT-3.1 (K8s API unreachable), RT-3.8 (metadata unreachable), RT-3.9 (cross-sandbox blocked) |
| G17 | `automountServiceAccountToken: false` | RT-3.2 (token dir absent + spec field confirmed) |

---

## New gaps surfaced by Phase 3

| ID | Title | Severity | Recommendation |
|---|---|---|---|
| **G21** | /sandbox-cfg/password mode 0644 (cp preserves source mode) | medium | `install -m 0600` in init script |
| **G22** | enableServiceLinks: true on workspace pod | low | Set `EnableServiceLinks: ptr(false)` in controller pod template |
| **G23** | /workspace PVC mount lacks `nosuid` | medium | Add `mountOptions: ["nosuid","nodev"]` to workspace storage class via Helm value |
| **G24** | No seccompProfile on workspace pod | low | Add `seccompProfile.type: RuntimeDefault` to pod-level securityContext |

(All four are added to [THREAT-MODEL.md](../THREAT-MODEL.md) revision history.)
