#!/usr/bin/env python3
"""
Phase 3 — Sandbox Isolation & Container Escape Harness
Epic 17 Pentest

Runs RT-3.1 .. RT-3.17 against live sandbox pods on `admin@home-kubernetes`.

Methodology
-----------
* Provisions two fresh `@pentest.local` users (alice, bob) via the public API.
* Creates one `base` workspace per user via the public API; lets the controller
  build the production pod spec (no hand-crafted YAML) — we test the real attack
  surface, not a mock.
* Each test is a function `rt_3_xx(ctx) -> Finding`. Findings are written to
  `phase-3/evidence/RT-3.xx.json` and aggregated to stdout.
* Inside-pod attacks use `kubectl exec`. The harness IS the attacker; the pod's
  shell IS the compromise.

Blast-radius rules
------------------
* Only @pentest.local accounts. No real-user pods are touched.
* Tests classified 🟢 / 🟡 / 🔴 in module docstrings. Default run skips 🔴.
* No probes against external attacker domains (we use `example.com` and the
  pod's own peer for the few cross-pod / DNS tests).

Result semantics (pentest perspective):
  PASS         — platform behaved correctly; no finding
  FAIL         — platform failed the security check; this IS a finding
  SKIP         — preconditions not met / quarantined
  INCONCLUSIVE — ran but needs human interpretation

Usage:
  API_BASE=http://127.0.0.1:19090 ./run-phase3.py             # safe tests
  API_BASE=... ./run-phase3.py --include-unsafe                # also 🔴 tests
  API_BASE=... ./run-phase3.py --only RT-3.4 RT-3.7            # targeted
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Callable, Optional

API_BASE = os.environ.get("API_BASE", "http://127.0.0.1:19090")
KCTX = os.environ.get("KUBECTL_CONTEXT", "admin@home-kubernetes")
NS = os.environ.get("PENTEST_NS", "default")
ARTEFACT_DIR = Path(__file__).resolve().parent.parent / "evidence"
ARTEFACT_DIR.mkdir(parents=True, exist_ok=True)


# ---------- HTTP helper -----------------------------------------------------


def http(method: str, path: str, *, json_body=None, headers=None, timeout=15):
    url = path if path.startswith("http") else f"{API_BASE}{path}"
    h = {"Accept": "application/json"}
    if headers:
        h.update(headers)
    body = None
    if json_body is not None:
        body = json.dumps(json_body).encode()
        h.setdefault("Content-Type", "application/json")
    req = urllib.request.Request(url, data=body, method=method, headers=h)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode(errors="replace"), dict(r.headers)
    except urllib.error.HTTPError as e:
        return (
            e.code,
            (e.read().decode(errors="replace") if e.fp else ""),
            dict(e.headers or {}),
        )
    except Exception as e:  # noqa: BLE001
        return 0, f"net-error: {e}", {}


# ---------- Subprocess helpers ----------------------------------------------


@dataclass
class Run:
    cmd: list
    rc: int
    stdout: str
    stderr: str
    duration_s: float


def run(cmd: list, *, timeout: int = 30, input_bytes: Optional[bytes] = None) -> Run:
    t0 = time.monotonic()
    try:
        p = subprocess.run(
            cmd,
            capture_output=True,
            timeout=timeout,
            input=input_bytes,
            check=False,
        )
        rc = p.returncode
        out = p.stdout.decode(errors="replace")
        err = p.stderr.decode(errors="replace")
    except subprocess.TimeoutExpired as e:
        rc = -1
        out = (e.stdout or b"").decode(errors="replace")
        err = f"TIMEOUT after {timeout}s: {e}"
    return Run(cmd, rc, out, err, time.monotonic() - t0)


def kctl(*args: str, timeout: int = 30) -> Run:
    return run(["kubectl", "--context", KCTX, "-n", NS, *args], timeout=timeout)


def pexec(pod: str, container: str, *cmd: str, timeout: int = 30) -> Run:
    """kubectl exec into a sandbox pod. The shell command is the attack."""
    return run(
        [
            "kubectl",
            "--context",
            KCTX,
            "-n",
            NS,
            "exec",
            pod,
            "-c",
            container,
            "--",
            *cmd,
        ],
        timeout=timeout,
    )


def psh(pod: str, container: str, script: str, *, timeout: int = 30) -> Run:
    """Run a shell script inside the sandbox via /bin/sh -c."""
    return pexec(pod, container, "/bin/sh", "-c", script, timeout=timeout)


# ---------- Account fixtures ------------------------------------------------


def deterministic_pw(seed: str) -> str:
    return "p3-" + hashlib.sha256(seed.encode()).hexdigest()[:24]


def register_or_login(username: str, email: str, password: str) -> Optional[str]:
    code, body, _ = http(
        "POST",
        "/api/v1/auth/register",
        json_body={"username": username, "email": email, "password": password},
    )
    if code in (200, 201):
        try:
            return json.loads(body).get("token")
        except Exception:
            pass
    # Fall through to login (maybe already registered).
    code, body, _ = http(
        "POST",
        "/api/v1/auth/login",
        json_body={"email": email, "password": password},
    )
    if code == 200:
        try:
            return json.loads(body).get("token")
        except Exception:
            return None
    return None


def create_workspace(token: str, *, runtime: str = "base") -> Optional[str]:
    code, body, _ = http(
        "POST",
        "/api/v1/workspaces",
        json_body={
            "runtime": runtime,
            "name": f"p3-{int(time.time())}",
            "storage": {"size": "1Gi"},
        },
        headers={"Authorization": f"Bearer {token}"},
    )
    if code in (200, 201):
        try:
            return json.loads(body)["id"]
        except Exception:
            return None
    print(f"  workspace create failed: {code} {body[:200]}", file=sys.stderr)
    return None


def wait_for_pod(workspace_id: str, *, timeout_s: int = 120) -> Optional[str]:
    """Poll the controller until the workspace pod is Running. Returns pod name."""
    deadline = time.monotonic() + timeout_s
    last = ""
    while time.monotonic() < deadline:
        r = kctl(
            "get",
            "workspace",
            workspace_id,
            "-o",
            "jsonpath={.status.podName}\t{.status.phase}",
        )
        if r.rc == 0 and "\t" in r.stdout:
            pod, phase = r.stdout.split("\t", 1)
            last = f"{pod}/{phase}"
            if phase.strip() == "Active" and pod.strip():
                # Confirm the pod itself is Running.
                pr = kctl("get", "pod", pod.strip(), "-o", "jsonpath={.status.phase}")
                if pr.rc == 0 and pr.stdout.strip() == "Running":
                    return pod.strip()
        time.sleep(2)
    print(f"  pod never reached Active; last={last}", file=sys.stderr)
    return None


# ---------- Finding model ---------------------------------------------------


@dataclass
class Finding:
    id: str
    title: str
    risk_class: str  # "🟢"|"🟡"|"🔴"
    result: str  # "PASS"|"FAIL"|"SKIP"|"INCONCLUSIVE"
    severity: str  # "info"|"low"|"medium"|"high"|"critical"
    expected: str
    observed: str
    evidence: list = field(default_factory=list)
    notes: str = ""

    def write(self):
        out = ARTEFACT_DIR / f"{self.id}.json"
        out.write_text(json.dumps(asdict(self), indent=2))


@dataclass
class Ctx:
    alice_pod: str
    bob_pod: str
    alice_pod_ip: str
    bob_pod_ip: str
    container: str = "workspace"


# ---------- Test cases (RT-3.x) ---------------------------------------------
#
# Risk classification from PHASE-3 plan validation:
#   🟢 safe-on-live       — pure read or self-contained inside one pod
#   🟡 caution-on-live    — touches PVC or kernel-adjacent surface
#   🔴 quarantine         — node-level blast radius, deferred to kind
#
# Each test must be self-explanatory: returns one Finding with concrete
# expected vs observed. No assertions outside the test body.


def rt_3_1(ctx: Ctx) -> Finding:
    """G16 fix: K8s API unreachable from sandbox."""
    # Use bash /dev/tcp for the TCP probe and getent for DNS — no python3 in
    # the base sandbox image. /dev/tcp returns 0 on connect, 1 on EHOSTUNREACH.
    script = """
set +e
ip=$(getent hosts kubernetes.default.svc.cluster.local | awk '{print $1}')
echo "RESOLVED=$ip"
if [ -z "$ip" ]; then echo "RC=NO_DNS"; exit 0; fi
timeout 3 bash -c "</dev/tcp/$ip/443" 2>&1
echo "RC=$?"
"""
    r = psh(ctx.alice_pod, ctx.container, script, timeout=15)
    expected = (
        "TCP connect to K8s API IP MUST fail (egress NetPol denies in-cluster CIDRs)"
    )
    if "RC=0" in r.stdout:
        return Finding(
            "RT-3.1",
            "K8s API reachable from sandbox",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"connected: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout, r.stderr],
        )
    if "RESOLVED=" in r.stdout and "RC=0" not in r.stdout:
        return Finding(
            "RT-3.1",
            "K8s API unreachable from sandbox (DNS resolves, TCP blocked)",
            "🟢",
            "PASS",
            "info",
            expected,
            f"resolved but no connect: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout, r.stderr],
            notes="G16 NetworkPolicy blocking 10.0.0.0/8 prevents the connect.",
        )
    return Finding(
        "RT-3.1",
        "K8s API probe inconclusive",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        f"unexpected output: {r.stdout!r} stderr={r.stderr!r}",
        evidence=[r.stdout, r.stderr],
    )
    r = psh(ctx.alice_pod, ctx.container, script, timeout=15)
    expected = (
        "TCP connect to K8s API IP MUST fail (egress NetPol denies in-cluster CIDRs)"
    )
    if "CONNECTED" in r.stdout:
        return Finding(
            "RT-3.1",
            "K8s API reachable from sandbox",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"connected: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout, r.stderr],
        )
    if "RESOLVED" in r.stdout and "CONNECTED" not in r.stdout:
        return Finding(
            "RT-3.1",
            "K8s API unreachable from sandbox (DNS resolves, TCP blocked)",
            "🟢",
            "PASS",
            "info",
            expected,
            f"resolved but no connect: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout, r.stderr],
            notes="G16 NetworkPolicy blocking 10.0.0.0/8 prevents the connect.",
        )
    return Finding(
        "RT-3.1",
        "K8s API probe inconclusive",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        f"unexpected output: {r.stdout!r} stderr={r.stderr!r}",
        evidence=[r.stdout, r.stderr],
    )


def rt_3_2(ctx: Ctx) -> Finding:
    """G17 fix: SA token directory is absent."""
    r = psh(
        ctx.alice_pod,
        ctx.container,
        "ls -la /var/run/secrets/kubernetes.io/ 2>&1; echo RC=$?",
    )
    expected = "/var/run/secrets/kubernetes.io/ MUST NOT exist (automountServiceAccountToken: false)"
    out = r.stdout
    if "No such file or directory" in out or "cannot access" in out or "RC=2" in out:
        # Cross-check via pod spec.
        spec = kctl(
            "get",
            "pod",
            ctx.alice_pod,
            "-o",
            "jsonpath={.spec.automountServiceAccountToken}",
        )
        return Finding(
            "RT-3.2",
            "SA token absent (G17 fix holds)",
            "🟢",
            "PASS",
            "info",
            expected,
            f"directory missing AND spec.automountServiceAccountToken={spec.stdout!r}",
            evidence=[out, spec.stdout],
        )
    if "token" in out:
        return Finding(
            "RT-3.2",
            "SA token mounted in sandbox",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"token present: {out[:300]}",
            evidence=[out],
        )
    return Finding(
        "RT-3.2",
        "SA token probe inconclusive",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        f"unexpected: {out[:300]}",
        evidence=[out],
    )


def rt_3_3(ctx: Ctx) -> Finding:
    """G3: env vars readable via /proc + service-link recon leak."""
    r = psh(
        ctx.alice_pod,
        ctx.container,
        "tr '\\0' '\\n' < /proc/1/environ 2>&1; echo RC=$?",
    )
    expected = (
        "Sensitive PID-1 env vars MUST NOT include cross-tenant credentials; "
        "service-link env (POD_PORT_TCP_*) leaks namespace topology — recommend "
        "enableServiceLinks: false on workspace pod"
    )
    # Threat model: the attacker is INSIDE the sandbox. Same-trust secrets
    # (opencode HTTP basic-auth password the attacker can already hit on
    # localhost) are NOT findings. Only credentials that grant access OUTSIDE
    # the sandbox boundary are findings.
    SAME_TRUST = {"OPENCODE_SERVER_PASSWORD"}  # used only for in-pod opencode
    cross_trust_secret_hits: list[str] = []
    same_trust_hits: list[str] = []
    service_link_hits: list[str] = []
    SECRET_PAT = (
        "KEY",
        "SECRET",
        "TOKEN",
        "PASSWORD",
        "DSN",
        "JWT",
        "API_KEY",
        "OPENAI",
        "ANTHROPIC",
        "DATABASE_URL",
        "PASSWD",
    )
    for line in r.stdout.splitlines():
        if "=" not in line:
            continue
        name = line.split("=", 1)[0]
        # Service-link env: <SVC>_PORT, <SVC>_PORT_<N>_TCP*, <SVC>_SERVICE_HOST/PORT
        if (
            name.endswith("_SERVICE_HOST")
            or name.endswith("_SERVICE_PORT")
            or name.startswith("KUBERNETES_")
            or "_PORT_" in name
            or name.endswith("_PORT")
        ):
            service_link_hits.append(line)
            continue
        if name in SAME_TRUST:
            same_trust_hits.append(name)
            continue
        if any(tok in name.upper() for tok in SECRET_PAT):
            cross_trust_secret_hits.append(line)
    if cross_trust_secret_hits:
        return Finding(
            "RT-3.3",
            "PID-1 env exposes cross-trust credentials",
            "🟢",
            "FAIL",
            "high",
            expected,
            "credentials in env: " + " | ".join(cross_trust_secret_hits[:10]),
            evidence=[r.stdout],
            notes=(
                "TRUE secret material in PID-1 env that grants access beyond "
                "the sandbox. Audit env injection."
            ),
        )
    if service_link_hits:
        return Finding(
            "RT-3.3",
            "Service-link env leaks namespace topology to sandbox",
            "🟢",
            "FAIL",
            "low",
            expected,
            (
                f"{len(service_link_hits)} service-link env vars present "
                f"(e.g. {service_link_hits[0]}); reveals all in-namespace service "
                f"IPs/ports to the sandbox. Same-trust hits ignored: {same_trust_hits}. "
                f"Pod spec lacks `enableServiceLinks: false`."
            ),
            evidence=[r.stdout],
            notes=(
                "Recon leak — sandbox learns IP/port of postgres, valkey, "
                "controller webhook, etc. Combined with NetPol egress (RT-3.1 "
                "blocks RFC1918 → these IPs aren't reachable anyway), "
                "currently low-impact, but trivially fixed: set "
                "EnableServiceLinks=ptr(false) in pod spec."
            ),
        )
    return Finding(
        "RT-3.3",
        "PID-1 env free of cross-trust secrets and service-links",
        "🟢",
        "PASS",
        "info",
        expected,
        f"clean env; same-trust env present: {same_trust_hits}",
        evidence=[r.stdout],
    )
    expected = (
        "Sensitive PID-1 env vars MUST NOT include credentials; service-link "
        "env (POD_PORT_TCP_*) leaks namespace topology — recommend "
        "enableServiceLinks: false on workspace pod"
    )
    secret_hits: list[str] = []
    service_link_hits: list[str] = []
    SECRET_PAT = (
        "KEY",
        "SECRET",
        "TOKEN",
        "PASSWORD",
        "DSN",
        "JWT",
        "API_KEY",
        "OPENAI",
        "ANTHROPIC",
        "DATABASE_URL",
    )
    for line in r.stdout.splitlines():
        if "=" not in line:
            continue
        name = line.split("=", 1)[0]
        # Service-link env: <SERVICE>_PORT, <SERVICE>_PORT_<NUM>_TCP*, <SERVICE>_SERVICE_HOST, <SERVICE>_SERVICE_PORT
        if (
            name.endswith("_SERVICE_HOST")
            or name.endswith("_SERVICE_PORT")
            or "_PORT_" in name
            or name.endswith("_PORT")
        ):
            service_link_hits.append(line)
            continue
        # Secret-shaped env (only flag if it's NOT a service-link false-positive)
        if any(tok in name.upper() for tok in SECRET_PAT):
            secret_hits.append(line)
    if secret_hits:
        return Finding(
            "RT-3.3",
            "PID-1 env exposes potentially sensitive variables",
            "🟢",
            "FAIL",
            "high",
            expected,
            "secret-shaped: " + " | ".join(secret_hits[:10]),
            evidence=[r.stdout],
            notes="True secret material in PID-1 env. Audit env injection.",
        )
    if service_link_hits:
        return Finding(
            "RT-3.3",
            "Service-link env leaks namespace topology to sandbox",
            "🟢",
            "FAIL",
            "low",
            expected,
            (
                f"{len(service_link_hits)} service-link env vars present "
                f"(e.g. {service_link_hits[0]}); reveals all in-namespace service "
                f"IPs/ports to the sandbox. NEW finding: pod spec lacks "
                f"`enableServiceLinks: false`."
            ),
            evidence=[r.stdout],
            notes=(
                "Recon leak — sandbox learns IP/port of postgres, valkey, "
                "controller webhook, etc. Combined with NetworkPolicy gaps "
                "(RT-3.1 result) this is currently low-impact, but trivially "
                "fixed: set EnableServiceLinks=ptr(false) in pod spec."
            ),
        )
    return Finding(
        "RT-3.3",
        "PID-1 env free of secrets and service-links",
        "🟢",
        "PASS",
        "info",
        expected,
        f"clean env; first lines: {r.stdout.splitlines()[:5]}",
        evidence=[r.stdout],
    )


def rt_3_4(ctx: Ctx) -> Finding:
    """G1 confirmed: writable+exec /tmp."""
    # Compile a tiny binary inside the pod? Unreliable across runtimes.
    # Instead: copy /bin/sh to /tmp/x, chmod +x, exec. /tmp is emptyDir.
    script = (
        "set -e; "
        "cp /bin/sh /tmp/x_pentest 2>&1; "
        "chmod +x /tmp/x_pentest 2>&1; "
        "/tmp/x_pentest -c 'echo PWNED-FROM-TMP' 2>&1; "
        "ls -l /tmp/x_pentest; "
        "stat -c '%A %u %g' /tmp/x_pentest; "
        "rm -f /tmp/x_pentest"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "Writable+exec /tmp confirmed (G1 — emptyDir lacks medium:Memory and noexec)"
    )
    if "PWNED-FROM-TMP" in r.stdout:
        return Finding(
            "RT-3.4",
            "Sandbox /tmp is writable AND exec-allowed",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"executed copy of /bin/sh from /tmp: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout, r.stderr],
            notes=(
                "Confirms G1. Remediation: emptyDir with medium:Memory and a "
                "RuntimeClass that mounts /tmp noexec. Trade-off: docs note that "
                "Python pip wheels and some toolchains rely on exec from /tmp."
            ),
        )
    return Finding(
        "RT-3.4",
        "Sandbox /tmp blocks exec (G1 mitigated?)",
        "🟢",
        "PASS",
        "info",
        expected,
        f"exec did not produce token: stdout={r.stdout!r} stderr={r.stderr!r}",
        evidence=[r.stdout, r.stderr],
    )


def rt_3_5(ctx: Ctx) -> Finding:
    """SUID escalation via /workspace PVC: should fail (nosuid mount)."""
    # We *can* write to /workspace (PVC). Try setting the SUID bit.
    # Even if the bit sets on disk, the kernel honours the mount option.
    script = (
        "set -e; "
        "cp /bin/sh /workspace/.suid_test 2>&1; "
        "chmod 4755 /workspace/.suid_test 2>&1; "
        "ls -l /workspace/.suid_test; "
        "stat -c '%A' /workspace/.suid_test; "
        # mount info for the PVC mount.
        "grep ' /workspace ' /proc/mounts 2>&1 || true; "
        "rm -f /workspace/.suid_test"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "PVC mount SHOULD include nosuid; SUID bit may set on disk but "
        "kernel must ignore it on exec"
    )
    has_nosuid = "nosuid" in r.stdout
    suid_set = r.stdout.count("rws") > 0 or r.stdout.startswith("-rws")
    if has_nosuid:
        return Finding(
            "RT-3.5",
            "/workspace mounted nosuid (defence-in-depth holds)",
            "🟡",
            "PASS",
            "info",
            expected,
            f"mount contains nosuid; suid_set={suid_set}: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.5",
        "/workspace mount missing nosuid",
        "🟡",
        "FAIL",
        "medium",
        expected,
        f"no nosuid in mount line; raw: {r.stdout.strip()[-300:]}",
        evidence=[r.stdout],
        notes="readOnlyRootFilesystem + cap-drop ALL still mitigate, but defence-in-depth weakened.",
    )


def rt_3_6(ctx: Ctx) -> Finding:
    """capabilities.drop: ALL — privileged ops must EPERM."""
    # The authoritative test is /proc/self/status: CapEff/CapBnd should be 0.
    # Bind-to-low-port is NOT a cap test on K8s post-1.22 because
    # net.ipv4.ip_unprivileged_port_start defaults to 0 — that's a sysctl,
    # not a capability. Probe each separately.
    raw_probe = (
        "perl -e 'use Socket; "
        'socket(S,PF_INET,SOCK_RAW,getprotobyname("icmp")) '
        'or die "RAW_FAIL:$!"; print "RAW_OK\\n"\' 2>&1'
    )
    script = (
        "set +e; "
        "echo ---CAPS---; cat /proc/self/status | grep -E '^(Cap|NoNewPrivs)'; "
        "echo ---UNPRIV_PORT_START---; cat /proc/sys/net/ipv4/ip_unprivileged_port_start; "
        f"echo ---RAW---; {raw_probe}; "
        "echo ---CHOWN---; chown 0:0 /tmp 2>&1; echo RC=$?; "
        "echo ---MKNOD---; mknod /tmp/devnull c 1 3 2>&1; echo RC=$?"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "CapEff/CapBnd MUST be 0; CAP_NET_RAW, CAP_CHOWN, CAP_MKNOD ops MUST "
        "fail. Low-port bind is governed by sysctl, not caps, in K8s>=1.22."
    )
    out = r.stdout
    failures = []
    if "CapEff:\t0000000000000000" not in out:
        failures.append("CapEff != 0")
    if "CapBnd:\t0000000000000000" not in out:
        failures.append("CapBnd != 0")
    if "NoNewPrivs:\t1" not in out:
        failures.append("NoNewPrivs != 1")
    if "RAW_OK" in out:
        failures.append("CAP_NET_RAW present (raw socket opened)")
    chown_section = (
        out.split("---CHOWN---", 1)[1].split("---", 1)[0]
        if "---CHOWN---" in out
        else ""
    )
    if "RC=0" in chown_section:
        failures.append("chown succeeded → CAP_CHOWN present")
    mknod_section = out.split("---MKNOD---", 1)[1] if "---MKNOD---" in out else ""
    if "RC=0" in mknod_section:
        failures.append("mknod succeeded → CAP_MKNOD present")
    if failures:
        return Finding(
            "RT-3.6",
            "Capability drop incomplete",
            "🟢",
            "FAIL",
            "high",
            expected,
            "; ".join(failures) + f" — raw: {out[:600]}",
            evidence=[out],
        )
    return Finding(
        "RT-3.6",
        "All capabilities denied (CapEff=0, NoNewPrivs=1)",
        "🟢",
        "PASS",
        "info",
        expected,
        f"raw probes blocked, caps zero; raw: {out[:600]}",
        evidence=[out],
        notes=(
            "Note: pod can bind to ports <1024 because K8s default sysctl "
            "ip_unprivileged_port_start=0. Default-deny ingress makes this "
            "moot for cross-tenant attacks; document as accepted."
        ),
    )
    raw_probe = (
        "perl -e 'use Socket; "
        'socket(S,PF_INET,SOCK_RAW,getprotobyname("icmp")) '
        'or die "RAW_FAIL:$!"; print "RAW_OK\\n"\' 2>&1'
    )
    script = (
        "set +e; "
        f"echo ---BIND80---; {bind_probe}; echo RC=$?; "
        f"echo ---RAW---; {raw_probe}; echo RC=$?; "
        "echo ---CHOWN---; chown 0:0 /tmp 2>&1; echo RC=$?; "
        "echo ---MKNOD---; mknod /tmp/devnull c 1 3 2>&1; echo RC=$?"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = "All capability-requiring ops MUST fail with EPERM"
    out = r.stdout
    failures = []
    if "BOUND" in out:
        failures.append("bind-to-80 succeeded → CAP_NET_BIND_SERVICE present")
    if "RAW_OK" in out:
        failures.append("raw socket open succeeded → CAP_NET_RAW present")
    chown_section = (
        out.split("---CHOWN---", 1)[1].split("---", 1)[0]
        if "---CHOWN---" in out
        else ""
    )
    if "RC=0" in chown_section:
        failures.append("chown succeeded → CAP_CHOWN present")
    mknod_section = out.split("---MKNOD---", 1)[1] if "---MKNOD---" in out else ""
    if "RC=0" in mknod_section:
        failures.append("mknod succeeded → CAP_MKNOD present")
    if failures:
        return Finding(
            "RT-3.6",
            "Capability drop incomplete",
            "🟢",
            "FAIL",
            "high",
            expected,
            "; ".join(failures),
            evidence=[out],
        )
    return Finding(
        "RT-3.6",
        "All probed capabilities denied",
        "🟢",
        "PASS",
        "info",
        expected,
        f"all probes blocked; raw: {out[:500]}",
        evidence=[out],
    )


def rt_3_7(ctx: Ctx) -> Finding:
    """No seccompProfile attached → blocked syscalls only blocked by caps."""
    # Confirm pod-level absence (already validated up-front; re-confirm here).
    spec = kctl(
        "get",
        "pod",
        ctx.alice_pod,
        "-o",
        "jsonpath={.spec.securityContext.seccompProfile}",
    )
    container_spec = kctl(
        "get",
        "pod",
        ctx.alice_pod,
        "-o",
        "jsonpath={.spec.containers[0].securityContext.seccompProfile}",
    )
    # Probe with perl: try unshare (needs CAP_SYS_ADMIN) and ptrace.
    syscall_probe = r"""perl -MConfig -e '
use strict;
sub try_call { my ($n, $name, @args) = @_;
  my $r = syscall($n, @args);
  printf "%s rc=%d errno=%s\n", $name, $r, ($r==-1?$!:"-");
}
# x86_64 syscall numbers
try_call(56, "clone");        # CAP_SYS_ADMIN for new namespaces (no flags = harmless)
try_call(101, "ptrace", 0,0,0,0);  # PTRACE_TRACEME
try_call(248, "add_key", 0,0,0,0,0);
try_call(250, "request_key", 0,0,0,0,0);
'"""
    script = (
        "set +e; "
        "echo ---UNSHARE---; unshare -U /bin/true 2>&1; echo RC=$?; "
        "echo ---SYSCALLS---; "
        f"{syscall_probe} 2>&1; echo RC=$?"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "Without seccomp, only cap-drop limits syscalls; some dangerous calls "
        "remain reachable. Document attack surface."
    )
    has_seccomp = bool(spec.stdout.strip()) or bool(container_spec.stdout.strip())
    if has_seccomp:
        return Finding(
            "RT-3.7",
            "Seccomp profile present",
            "🟡",
            "PASS",
            "info",
            expected,
            f"pod={spec.stdout!r} container={container_spec.stdout!r}",
            evidence=[spec.stdout, container_spec.stdout, r.stdout],
        )
    # No seccomp — but cap-drop+NoNewPrivs may already block dangerous calls.
    # Look for any probe that succeeded; if all are EPERM, severity is LOW
    # (defence-in-depth gap), not MEDIUM (currently exploitable).
    out = r.stdout
    any_succeeded = False
    for line in out.splitlines():
        # Format from the probe: "name rc=N errno=..."
        if (
            "rc=" in line
            and "errno=Operation not permitted" not in line
            and "rc=-1" not in line
        ):
            any_succeeded = True
            break
    severity = "medium" if any_succeeded else "low"
    return Finding(
        "RT-3.7",
        "No seccomp profile attached to sandbox pod",
        "🟡",
        "FAIL",
        severity,
        expected,
        (
            f"no seccompProfile in pod or container spec; "
            f"any_dangerous_syscall_succeeded={any_succeeded}; "
            f"syscall probes: {out[:600]}"
        ),
        evidence=[spec.stdout, container_spec.stdout, r.stdout],
        notes=(
            "Recommend RuntimeDefault profile at pod level. "
            "Currently, cap-drop ALL + NoNewPrivs:1 already EPERM the probed "
            "syscalls, so severity is LOW (defence-in-depth) unless a "
            "subsequent finding restores any caps."
        ),
    )


def rt_3_8(ctx: Ctx) -> Finding:
    """G16 fix: 169.254.169.254 metadata blocked."""
    script = "timeout 3 bash -c '</dev/tcp/169.254.169.254/80' 2>&1; echo RC=$?"
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = "169.254.169.254:80 MUST be unreachable (G16 blockedEgressCIDRs)"
    if "RC=0" in r.stdout:
        return Finding(
            "RT-3.8",
            "Cloud metadata IP reachable",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"connected: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.8",
        "Cloud metadata IP unreachable (G16 holds)",
        "🟢",
        "PASS",
        "info",
        expected,
        f"connect failed as expected: {r.stdout.strip()[-200:]}",
        evidence=[r.stdout],
        notes=(
            "On Talos this is also blocked at L3 by the node firewall, so this "
            "test does not isolate the NetworkPolicy. Re-run on cloud K8s to "
            "fully validate."
        ),
    )


def rt_3_9(ctx: Ctx) -> Finding:
    """G16 fix: cross-pod traffic denied."""
    script = f"timeout 3 bash -c '</dev/tcp/{ctx.bob_pod_ip}/8080' 2>&1; echo RC=$?"
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = "Sandbox A → Sandbox B agentd MUST fail (default-deny ingress)"
    if "RC=0" in r.stdout:
        return Finding(
            "RT-3.9",
            "Cross-sandbox connectivity present",
            "🟢",
            "FAIL",
            "critical",
            expected,
            f"alice connected to bob's pod IP {ctx.bob_pod_ip}:8080: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.9",
        "Cross-sandbox connectivity denied (G16 holds)",
        "🟢",
        "PASS",
        "info",
        expected,
        f"connect failed: {r.stdout.strip()[-200:]}",
        evidence=[r.stdout],
    )


def rt_3_10(ctx: Ctx) -> Finding:
    """DNS exfil possible (accepted) — verify and document."""
    # getent hosts uses NSS; resolves via /etc/resolv.conf → kube-dns.
    # CoreDNS forwards externally; we expect a real answer (v4 OR v6).
    script = "set +e; getent hosts example.com 2>&1; echo RC=$?"
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "Public DNS resolution succeeds (accepted risk: sandbox needs DNS for "
        "package installs); document audit-log expectation"
    )
    # A successful answer always begins with an IP token (v4 dotted quad OR v6
    # with at least two ':' chars).
    success = False
    for line in r.stdout.splitlines():
        if not line or line.startswith("RC="):
            continue
        first = line.split()[0] if line.split() else ""
        if (first.count(".") == 3) or (first.count(":") >= 2):
            success = True
            break
    if success and "RC=0" in r.stdout:
        return Finding(
            "RT-3.10",
            "Public DNS resolution permitted (accepted)",
            "🟢",
            "PASS",
            "info",
            expected,
            f"resolved example.com: {r.stdout.strip()[:300]}",
            evidence=[r.stdout],
            notes=(
                "Risk: arbitrary DNS exfil possible via in-cluster CoreDNS "
                "forwarder. Mitigation options: "
                "(a) restrict egress to a named DNS allow-list via Cilium "
                "FQDN policy; (b) run a logging resolver. Currently neither."
            ),
        )
    return Finding(
        "RT-3.10",
        "Public DNS resolution failed (unexpected)",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        f"unexpected: {r.stdout[:300]}",
        evidence=[r.stdout],
    )
    # getent prints "<ip> <name>" on success
    first = r.stdout.splitlines()[0] if r.stdout else ""
    if first and first.split()[0].count(".") == 3 and "RC=0" in r.stdout:
        return Finding(
            "RT-3.10",
            "Public DNS resolution permitted (accepted)",
            "🟢",
            "PASS",
            "info",
            expected,
            f"resolved example.com: {first[:200]}",
            evidence=[r.stdout],
            notes=(
                "Risk: arbitrary DNS exfil possible. Mitigation options: "
                "(a) restrict egress to a named DNS allow-list via Cilium "
                "FQDN policy; (b) run a logging resolver. Currently neither."
            ),
        )
    return Finding(
        "RT-3.10",
        "Public DNS resolution failed (unexpected)",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        f"unexpected: {r.stdout[:300]}",
        evidence=[r.stdout],
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = "169.254.169.254:80 MUST be unreachable (G16 blockedEgressCIDRs)"
    if "CONNECTED" in r.stdout:
        return Finding(
            "RT-3.8",
            "Cloud metadata IP reachable",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"connected: {r.stdout.strip()[-200:]}",
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.8",
        "Cloud metadata IP unreachable (G16 holds)",
        "🟢",
        "PASS",
        "info",
        expected,
        f"connect failed as expected: {r.stdout.strip()[-200:]}",
        evidence=[r.stdout],
        notes=(
            "On Talos this is also blocked at L3 by the node firewall, so this "
            "test does not isolate the NetworkPolicy. Re-run on cloud K8s to "
            "fully validate."
        ),
    )


def rt_3_11(ctx: Ctx) -> Finding:
    """Resource exhaustion — QUARANTINED on live cluster."""
    return Finding(
        "RT-3.11",
        "Resource exhaustion (fork bomb / memory / disk)",
        "🔴",
        "SKIP",
        "info",
        "Sandbox cgroup limits should contain fork bomb / memory pressure",
        "Quarantined: node-level blast radius. Defer to dedicated kind cluster.",
        notes="Static check: pod has resources.limits set? See RT-3.11 evidence.",
    )


def rt_3_12(ctx: Ctx) -> Finding:
    """PID namespace isolation: cannot signal processes outside pod."""
    # Try to kill init of the host (would have PID 1 in host ns).
    # Inside a PID-namespaced container, /proc only shows our own pids;
    # `kill -0 <huge pid>` should ESRCH.
    script = (
        "set +e; "
        "ls /proc/ | grep -E '^[0-9]+$' | wc -l; "
        # Pick a PID that does not exist in our ns.
        "kill -0 99999 2>&1; echo RC=$?"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "PID namespace isolation: only own pids visible; cross-ns signaling impossible"
    )
    visible = r.stdout.splitlines()[0] if r.stdout else ""
    rc_line = [l for l in r.stdout.splitlines() if l.startswith("RC=")]
    rc = rc_line[0] if rc_line else "RC=?"
    # We expect very few PIDs (less than ~50) and RC=1 (ESRCH).
    if rc == "RC=1":
        return Finding(
            "RT-3.12",
            "PID namespace isolated",
            "🟢",
            "PASS",
            "info",
            expected,
            f"visible_pid_count={visible} kill_rc={rc}",
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.12",
        "PID namespace probe inconclusive",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        f"visible={visible} rc={rc}",
        evidence=[r.stdout],
    )


def rt_3_13(ctx: Ctx) -> Finding:
    """Symlink to /etc/shadow — readOnlyRootFilesystem mitigates."""
    # /etc/shadow is on the rootfs. The container has readOnlyRootFilesystem,
    # so /etc is read-only. /workspace is writable; create symlink there.
    script = (
        "set +e; "
        "ln -sf /etc/shadow /workspace/.shadowlink 2>&1; "
        "cat /workspace/.shadowlink 2>&1; echo RC=$?; "
        "rm -f /workspace/.shadowlink"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "/etc/shadow either non-existent (distroless), or readable but empty/"
        "useless (sandbox uid 1000 has no shadow entry)"
    )
    if "Permission denied" in r.stdout or "No such file" in r.stdout:
        return Finding(
            "RT-3.13",
            "/etc/shadow not readable from sandbox",
            "🟢",
            "PASS",
            "info",
            expected,
            r.stdout.strip()[:200],
            evidence=[r.stdout],
        )
    if "root:" in r.stdout or "$" in r.stdout:
        return Finding(
            "RT-3.13",
            "/etc/shadow readable from sandbox",
            "🟢",
            "FAIL",
            "high",
            expected,
            f"shadow content visible: {r.stdout.strip()[:300]}",
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.13",
        "/etc/shadow probe inconclusive",
        "🟢",
        "INCONCLUSIVE",
        "info",
        expected,
        r.stdout.strip()[:300],
        evidence=[r.stdout],
    )


def rt_3_14(ctx: Ctx) -> Finding:
    """Device files blocked: /dev/kmsg, /dev/mem unreadable / nonexistent."""
    script = (
        "set +e; "
        "for d in /dev/kmsg /dev/mem /dev/sda /dev/disk; do "
        '  echo "---$d---"; '
        "  ls -l $d 2>&1; "
        "  head -c 1 $d 2>&1 >/dev/null && echo OPENED || echo BLOCKED; "
        "done; "
        "echo ---listing---; ls -1 /dev/"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = "Sensitive device files MUST be absent or unreadable from the sandbox"
    if "OPENED" in r.stdout:
        return Finding(
            "RT-3.14",
            "Sensitive /dev entry openable from sandbox",
            "🟢",
            "FAIL",
            "high",
            expected,
            r.stdout.strip()[-400:],
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.14",
        "Sensitive /dev entries blocked",
        "🟢",
        "PASS",
        "info",
        expected,
        f"all probes blocked; /dev listing: {r.stdout.strip()[-400:]}",
        evidence=[r.stdout],
    )


def rt_3_15(ctx: Ctx) -> Finding:
    """G15: plaintext secrets on node disk — node access required, deferred."""
    return Finding(
        "RT-3.15",
        "Plaintext secrets on node disk (G15)",
        "🔴",
        "SKIP",
        "info",
        "emptyDir for sandbox-cfg lives on node disk; secrets readable by node compromise",
        (
            "Skipped: node-shell required, off-scope per blast-radius rules. "
            "Static finding: pod spec confirms `sandbox-cfg` is `emptyDir{}` (no "
            "medium:Memory) — plaintext lands on node ext4 until reclaim. "
            "G15 stands."
        ),
        notes="Treat as confirmed static finding; defer dynamic exploit to dedicated kind cluster.",
    )


def rt_3_16(ctx: Ctx) -> Finding:
    """G20 fix: secrets file mode 0600 atomic with creation."""
    # The G20 fix lives in pkg/agentd/secrets which materialises into
    # /sandbox-cfg/. Inspect every regular file there for mode 0600.
    script = "find /sandbox-cfg -type f -exec stat -c '%a %u %n' {} + 2>&1 || true"
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = "All files under /sandbox-cfg MUST be mode 600, owner uid 1000"
    bad = []
    for line in r.stdout.splitlines():
        parts = line.split()
        if len(parts) < 3:
            continue
        mode, uid, *_ = parts
        if mode != "600":
            bad.append(line)
        if uid != "1000":
            bad.append(line + "  (wrong uid)")
    if not r.stdout.strip():
        return Finding(
            "RT-3.16",
            "/sandbox-cfg empty or unreadable",
            "🟢",
            "INCONCLUSIVE",
            "info",
            expected,
            "no files listed; possibly not yet materialised",
            evidence=[r.stdout, r.stderr],
        )
    if bad:
        return Finding(
            "RT-3.16",
            "/sandbox-cfg files have wrong mode",
            "🟢",
            "FAIL",
            "medium",
            expected,
            "violations: " + "; ".join(bad[:10]),
            evidence=[r.stdout],
        )
    return Finding(
        "RT-3.16",
        "All /sandbox-cfg files mode 600 uid 1000 (G20 fix holds)",
        "🟢",
        "PASS",
        "info",
        expected,
        r.stdout.strip()[:600],
        evidence=[r.stdout],
    )


def rt_3_17(ctx: Ctx) -> Finding:
    """Mise binary tampering: writable runtime persists across pod restart."""
    # We don't actually trigger a suspend/resume here; we statically prove the
    # writability of the mise install path inside /workspace, which IS the PVC,
    # which IS persisted across pod recreation. That is the whole vulnerability
    # surface; the suspend/resume cycle is just the trigger.
    script = (
        "set +e; "
        "ls -ld /workspace/.local/share/mise/installs 2>&1 || echo NO_MISE_DIR; "
        # Try writing a tampered file.
        "mkdir -p /workspace/.local/share/mise/installs/_pentest 2>&1; "
        "echo tampered > /workspace/.local/share/mise/installs/_pentest/marker 2>&1; "
        "ls -l /workspace/.local/share/mise/installs/_pentest 2>&1; "
        "rm -rf /workspace/.local/share/mise/installs/_pentest"
    )
    r = psh(ctx.alice_pod, ctx.container, script)
    expected = (
        "Mise install dir writable by sandbox user; tampered binary would "
        "survive pod restarts via PVC. Document as accepted (mise design) "
        "or recommend integrity check."
    )
    if "tampered" in r.stdout or "marker" in r.stdout:
        return Finding(
            "RT-3.17",
            "Mise install path writable from sandbox",
            "🟡",
            "FAIL",
            "medium",
            expected,
            f"successfully wrote tampered marker: {r.stdout.strip()[-300:]}",
            evidence=[r.stdout],
            notes=(
                "By design (user installs runtimes), but documented attack: "
                "supply-chain pivot if a runtime binary is tampered after "
                "first install. Recommend optional `mise verify` on resume."
            ),
        )
    if "NO_MISE_DIR" in r.stdout:
        return Finding(
            "RT-3.17",
            "Mise install dir not yet present (no runtimes installed)",
            "🟡",
            "INCONCLUSIVE",
            "info",
            expected,
            r.stdout.strip()[:300],
            evidence=[r.stdout],
            notes="Re-run after `mise install python` in fixture.",
        )
    return Finding(
        "RT-3.17",
        "Mise tampering probe inconclusive",
        "🟡",
        "INCONCLUSIVE",
        "info",
        expected,
        r.stdout.strip()[:300],
        evidence=[r.stdout],
    )


# ---------- Test registry ---------------------------------------------------


TESTS: dict[str, Callable[[Ctx], Finding]] = {
    "RT-3.1": rt_3_1,
    "RT-3.2": rt_3_2,
    "RT-3.3": rt_3_3,
    "RT-3.4": rt_3_4,
    "RT-3.5": rt_3_5,
    "RT-3.6": rt_3_6,
    "RT-3.7": rt_3_7,
    "RT-3.8": rt_3_8,
    "RT-3.9": rt_3_9,
    "RT-3.10": rt_3_10,
    "RT-3.11": rt_3_11,
    "RT-3.12": rt_3_12,
    "RT-3.13": rt_3_13,
    "RT-3.14": rt_3_14,
    "RT-3.15": rt_3_15,
    "RT-3.16": rt_3_16,
    "RT-3.17": rt_3_17,
}

UNSAFE_IDS = {"RT-3.11", "RT-3.15"}


# ---------- Orchestration ---------------------------------------------------


def provision(label: str, idx: int) -> tuple[Optional[str], Optional[str]]:
    """Returns (workspace_id, pod_name)."""
    # Email-derived seed: same email always → same password, so re-running the
    # harness picks up where it left off without lockout collisions.
    email = f"phase3-{label}@pentest.local"
    pw = deterministic_pw(f"phase3-fixed-seed::{email}")
    print(f"  provisioning {email} ...", file=sys.stderr)
    token = register_or_login(f"phase3-{label}", email, pw)
    if not token:
        print(f"  ERROR: could not get token for {email}", file=sys.stderr)
        return None, None
    ws = create_workspace(token, runtime="base")
    if not ws:
        return None, None
    pod = wait_for_pod(ws, timeout_s=180)
    return ws, pod


def get_pod_ip(pod: str) -> str:
    r = kctl("get", "pod", pod, "-o", "jsonpath={.status.podIP}")
    return r.stdout.strip()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--include-unsafe", action="store_true")
    ap.add_argument("--only", nargs="+", help="Run only these test IDs")
    ap.add_argument(
        "--keep-workspaces",
        action="store_true",
        help="Don't delete workspaces at end (for debugging)",
    )
    args = ap.parse_args()

    print(f"=== Phase 3 harness ===", file=sys.stderr)
    print(f"  API_BASE = {API_BASE}", file=sys.stderr)
    print(f"  context  = {KCTX}", file=sys.stderr)
    print(f"  ns       = {NS}", file=sys.stderr)
    print(f"  artefacts= {ARTEFACT_DIR}", file=sys.stderr)

    # Provision two sandboxes.
    print("\n--- provisioning alice ---", file=sys.stderr)
    alice_ws, alice_pod = provision("alice", 0)
    print("\n--- provisioning bob ---", file=sys.stderr)
    bob_ws, bob_pod = provision("bob", 1)

    if not all([alice_ws, alice_pod, bob_ws, bob_pod]):
        print("PROVISIONING FAILED", file=sys.stderr)
        sys.exit(2)
    assert alice_ws and alice_pod and bob_ws and bob_pod  # narrow types

    alice_ip = get_pod_ip(alice_pod)
    bob_ip = get_pod_ip(bob_pod)
    print(f"  alice: {alice_pod} @ {alice_ip}", file=sys.stderr)
    print(f"  bob:   {bob_pod} @ {bob_ip}", file=sys.stderr)
    if not (alice_ip and bob_ip):
        print("POD IPs MISSING", file=sys.stderr)
        sys.exit(2)

    ctx = Ctx(alice_pod, bob_pod, alice_ip, bob_ip)

    # Determine test set.
    ids = list(TESTS.keys())
    if args.only:
        ids = [i for i in ids if i in args.only]
    if not args.include_unsafe:
        ids = [i for i in ids if i not in UNSAFE_IDS]

    print(f"\n--- running {len(ids)} tests ---", file=sys.stderr)
    findings: list[Finding] = []
    for tid in ids:
        print(f"  {tid} ...", file=sys.stderr)
        try:
            f = TESTS[tid](ctx)
        except Exception as e:  # noqa: BLE001
            f = Finding(
                tid,
                f"harness error in {tid}",
                "🟢",
                "INCONCLUSIVE",
                "info",
                "test should run cleanly",
                f"exception: {e}",
                notes=str(e),
            )
        f.write()
        findings.append(f)
        print(f"    {f.result:13} {f.severity:8} {f.title}", file=sys.stderr)

    # Summary table.
    counts = {"PASS": 0, "FAIL": 0, "SKIP": 0, "INCONCLUSIVE": 0}
    for f in findings:
        counts[f.result] = counts.get(f.result, 0) + 1
    print("\n=== Summary ===", file=sys.stderr)
    for k in ("PASS", "FAIL", "INCONCLUSIVE", "SKIP"):
        print(f"  {k:13} {counts.get(k, 0)}", file=sys.stderr)

    # Aggregate JSON to stdout.
    json.dump([asdict(f) for f in findings], sys.stdout, indent=2)
    print()

    # Cleanup.
    if not args.keep_workspaces:
        for ws in (alice_ws, bob_ws):
            kctl("delete", "workspace", ws, "--ignore-not-found", timeout=60)


if __name__ == "__main__":
    main()
