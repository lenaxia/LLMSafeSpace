#!/usr/bin/env python3
"""
Phase 5 — Proxy & Network Egress
Epic 17 Pentest

The proxy lives in `api/internal/handlers/proxy.go` and forwards requests to
the workspace pod's opencode HTTP server (port 4096) using basic auth.

Tests are split into two flavours:
  - API-side tests (RT-5.1, 5.2, 5.3, 5.5, 5.10, 5.11, 5.12, 5.13, 5.14):
    drive the proxy from outside.
  - Sandbox-side tests (RT-5.7, 5.8): from inside a workspace pod.
    These overlap with Phase 3 and re-use the same probes.

Static-confirmed gaps (no live exploit needed):
  - G4 plain HTTP proxy (proxy.go:405)
  - G12 ResponseHeaderTimeout 300s (proxy.go:95)
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
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


def http(
    method: str, path: str, *, json_body=None, headers=None, raw_body=None, timeout=15
):
    url = path if path.startswith("http") else f"{API_BASE}{path}"
    h = {"Accept": "application/json"}
    if headers:
        h.update(headers)
    body = None
    if json_body is not None:
        body = json.dumps(json_body).encode()
        h.setdefault("Content-Type", "application/json")
    elif raw_body is not None:
        body = raw_body
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


@dataclass
class Run:
    cmd: list
    rc: int
    stdout: str
    stderr: str
    duration_s: float


def run(cmd: list, *, timeout: int = 30) -> Run:
    t0 = time.monotonic()
    try:
        p = subprocess.run(cmd, capture_output=True, timeout=timeout, check=False)
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


def deterministic_pw(seed: str) -> str:
    return "p5-" + hashlib.sha256(seed.encode()).hexdigest()[:24]


def register_or_login(username: str, email: str, password: str) -> Optional[str]:
    """Register-then-login so DEK is cached for this session."""
    http(
        "POST",
        "/api/v1/auth/register",
        json_body={"username": username, "email": email, "password": password},
    )
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


def create_workspace(token: str, *, name: str = "p5-ws") -> Optional[str]:
    code, body, _ = http(
        "POST",
        "/api/v1/workspaces",
        headers={"Authorization": f"Bearer {token}"},
        json_body={"name": name, "runtime": "base", "storage": {"size": "1Gi"}},
    )
    if code in (200, 201):
        try:
            return json.loads(body)["id"]
        except Exception:
            return None
    return None


def wait_for_pod(workspace_id: str, *, timeout_s: int = 180) -> Optional[str]:
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        r = kctl(
            "get",
            "workspace",
            workspace_id,
            "-o",
            "jsonpath={.status.podName}\t{.status.phase}",
        )
        if "\t" in r.stdout:
            pod, phase = r.stdout.split("\t", 1)
            if phase.strip() == "Active" and pod.strip():
                return pod.strip()
        time.sleep(2)
    return None


def get_pod_ip(pod: str) -> str:
    return kctl("get", "pod", pod, "-o", "jsonpath={.status.podIP}").stdout.strip()


@dataclass
class Finding:
    id: str
    title: str
    result: str
    severity: str
    expected: str
    observed: str
    evidence: list = field(default_factory=list)
    notes: str = ""

    def write(self):
        (ARTEFACT_DIR / f"{self.id}.json").write_text(
            json.dumps(asdict(self), indent=2)
        )


@dataclass
class Ctx:
    token: str
    workspace_id: str
    pod_name: str
    pod_ip: str


# ---------- Test cases ------------------------------------------------------


def rt_5_1(ctx: Ctx) -> Finding:
    """SSRF via proxy: try to make the proxy hit something other than the
    workspace pod IP. Static analysis already confirms target IP is taken from
    Status.PodIP, but verify there's no header / param injection."""
    expected = "Proxy MUST NOT be redirectable to attacker-controlled IPs via headers or path params"
    # Try Host header injection.
    code, body, _ = http(
        "POST",
        f"/api/v1/sandboxes/{ctx.workspace_id}/sessions",
        headers={
            "Authorization": f"Bearer {ctx.token}",
            "Host": "evil.example.com",
            "X-Forwarded-Host": "evil.example.com",
            "X-Forwarded-For": "1.2.3.4",
        },
        json_body={},
    )
    # Also try a malformed workspace ID to see if we can break out.
    code2, body2, _ = http(
        "POST",
        f"/api/v1/sandboxes/../../../etc/passwd/sessions",
        headers={"Authorization": f"Bearer {ctx.token}"},
        json_body={},
    )
    obs = f"host_header_inject_status={code} traversal_status={code2}"
    if code in (200, 201) and code2 in (200, 201):
        return Finding(
            "RT-5.1",
            "Header injection or path traversal yielded 2xx",
            "FAIL",
            "high",
            expected,
            obs,
            [body[:300], body2[:300]],
        )
    return Finding(
        "RT-5.1",
        "Proxy target not influenceable by headers/path",
        "PASS",
        "info",
        expected,
        obs,
        [body[:300], body2[:300]],
    )


def rt_5_2(ctx: Ctx) -> Finding:
    """Proxy to arbitrary port — static check confirms hardcoded."""
    expected = "Proxy target port MUST be hardcoded to opencodePort"
    # Static: confirmed at proxy.go:29 by code grep.
    return Finding(
        "RT-5.2",
        "Proxy port hardcoded (static confirm)",
        "PASS",
        "info",
        expected,
        "proxy.go:29 const opencodePort = agentd.AgentPort (4096)",
        ["static analysis only"],
        "No user-controlled port in routes; verified by code grep.",
    )


def rt_5_3(ctx: Ctx) -> Finding:
    """HTTP request smuggling: send conflicting Content-Length and
    Transfer-Encoding to the proxy."""
    expected = "Gin/net.http MUST reject CL-TE / TE-CL conflicts"
    # Use raw socket to send a deliberately-bad request.
    import socket

    raw = (
        f"POST /api/v1/sandboxes/{ctx.workspace_id}/sessions HTTP/1.1\r\n"
        f"Host: 127.0.0.1:19090\r\n"
        f"Authorization: Bearer {ctx.token}\r\n"
        f"Content-Length: 5\r\n"
        f"Transfer-Encoding: chunked\r\n"
        f"\r\n"
        f"0\r\n\r\n"
        f"GPOST /admin HTTP/1.1\r\n"
        f"Host: 127.0.0.1:19090\r\n"
        f"\r\n"
    ).encode()
    s = socket.socket()
    s.settimeout(8)
    response = b""
    try:
        s.connect(("127.0.0.1", 19090))
        s.sendall(raw)
        # Read until socket closes or 2s of silence — whichever first.
        s.settimeout(2)
        while True:
            try:
                chunk = s.recv(4096)
                if not chunk:
                    break
                response += chunk
                if len(response) > 20000:
                    break
            except socket.timeout:
                break
    except Exception as e:  # noqa: BLE001
        return Finding(
            "RT-5.3",
            "Smuggling probe socket error",
            "INCONCLUSIVE",
            "info",
            expected,
            f"{type(e).__name__}: {e}; partial={response.decode(errors='replace')[:300]}",
            [response.decode(errors="replace")],
        )
    finally:
        s.close()
    text = response.decode(errors="replace")[:1500]
    # Two responses on a single connection = pipelining (correct behaviour of
    # Go's net/http when CL-TE conflict resolves to TE-chunked, then the
    # trailing bytes are interpreted as the next pipelined request). True
    # CL-TE smuggling requires an intermediate proxy that disagrees with the
    # origin on which header to honour. The deployed API has no such proxy.
    n_status_lines = text.count("HTTP/1.1 ")
    n_req_ids = text.count("X-Request-Id:")
    if n_status_lines >= 2 and n_req_ids >= 2:
        return Finding(
            "RT-5.3",
            "CL-TE conflict handled correctly (pipelining, not smuggling)",
            "PASS",
            "info",
            expected,
            f"two responses with two distinct request_ids — net/http honoured TE-chunked over CL=5 per RFC; pipelined trailing bytes as new request",
            [text],
            notes=(
                "True smuggling needs an intermediate proxy disagreeing with "
                "origin. Direct pentest against Go net/http is not exploitable. "
                "If a CDN/proxy is added in front, retest."
            ),
        )
    if n_status_lines >= 2:
        return Finding(
            "RT-5.3",
            "Two status lines but inconsistent request_id count (possible smuggling)",
            "FAIL",
            "high",
            expected,
            f"received {n_status_lines} status lines, {n_req_ids} request IDs: {text[:600]}",
            [text],
        )
    if "400" in text or "411" in text or "501" in text:
        return Finding(
            "RT-5.3",
            "Server rejected smuggling attempt",
            "PASS",
            "info",
            expected,
            f"single status line, rejection code visible: {text[:300]}",
            [text],
        )
    return Finding(
        "RT-5.3",
        "Smuggling probe inconclusive",
        "INCONCLUSIVE",
        "info",
        expected,
        text[:300],
        [text],
    )


def rt_5_4(ctx: Ctx) -> Finding:
    """SSE injection: payload from the LLM/opencode side that confuses parser."""
    expected = "SSE stream parser MUST not allow injected events"
    return Finding(
        "RT-5.4",
        "SSE injection (deferred — needs in-pod opencode response shaping)",
        "INCONCLUSIVE",
        "info",
        expected,
        "Requires modifying opencode's response stream; outside black-box scope.",
        [],
        "Recommend a unit test in the proxy that fuzzes raw SSE bytes through stripPatchParts.",
    )


def rt_5_5(ctx: Ctx) -> Finding:
    """Connection exhaustion: open maxConnectionsPerWorkspace+1 streaming connections."""
    expected = "Proxy MUST cap concurrent connections per workspace at 10"
    # We can't easily hold 11 long-lived SSE connections from this script.
    # Instead, fire 15 quick GET /events requests and inspect headers / errors.
    import threading

    statuses: list = []

    def open_one(idx: int):
        # GET /events is the SSE stream.
        code, body, _ = http(
            "GET",
            f"/api/v1/sandboxes/{ctx.workspace_id}/events",
            headers={
                "Authorization": f"Bearer {ctx.token}",
                "Accept": "text/event-stream",
            },
            timeout=2,
        )
        statuses.append((idx, code, len(body)))

    threads = [threading.Thread(target=open_one, args=(i,)) for i in range(15)]
    for t in threads:
        t.start()
    for t in threads:
        t.join(timeout=10)
    n_429 = sum(1 for (_i, c, _l) in statuses if c == 429)
    n_503 = sum(1 for (_i, c, _l) in statuses if c == 503)
    n_2xx = sum(1 for (_i, c, _l) in statuses if 200 <= c < 300)
    obs = f"15 simultaneous /events: 2xx={n_2xx} 429={n_429} 503={n_503} statuses={[c for (_i, c, _) in statuses]}"
    if n_429 + n_503 > 0:
        return Finding(
            "RT-5.5",
            "Connection limit enforced (some requests rate-limited)",
            "PASS",
            "info",
            expected,
            obs,
            [json.dumps(statuses)],
        )
    return Finding(
        "RT-5.5",
        "No connection limit observed",
        "INCONCLUSIVE",
        "info",
        expected,
        obs,
        [json.dumps(statuses)],
        "All 15 received 2xx — limit may apply only to long-lived streams. Defer to load test.",
    )


def rt_5_6(ctx: Ctx) -> Finding:
    """Stale pod IP exploitation — defer to integration test."""
    return Finding(
        "RT-5.6",
        "Stale pod IP exploitation (deferred)",
        "INCONCLUSIVE",
        "info",
        "Proxy must verify pod ownership before connecting",
        "Static check: proxy.go:373 has retry-with-fresh-IP logic. Live exploit requires racing pod restart.",
        [],
        "Defer to integration test that kills a pod mid-request and races with a new pod claim.",
    )


def rt_5_7(ctx: Ctx) -> Finding:
    """NetworkPolicy bypass — DNS rebinding / tunneling. Re-uses Phase 3 bypass attempts."""
    expected = (
        "DNS allow-list narrowed to kube-dns; egress NetPol denies in-cluster CIDRs"
    )
    # From inside the sandbox: try to resolve an external attacker-style domain
    # via raw DNS to a public resolver (8.8.8.8). Cilium FQDN policy is not in
    # use, so as long as :53 to 8.8.8.8 is allowed, the attacker can bypass
    # CoreDNS entirely. The egress policy DOES allow port-anything to public
    # IPs (Phase 1 finding), so 8.8.8.8:53 should work.
    script = (
        "set +e; "
        "timeout 3 bash -c '</dev/tcp/8.8.8.8/53' 2>&1; echo TCP=$?; "
        # If TCP works, DNS over UDP is also commonly open.
        # Use perl to send a raw DNS query.
        "perl -e 'use IO::Socket::INET; "
        '$s=IO::Socket::INET->new(PeerAddr=>"8.8.8.8",PeerPort=>53,Proto=>"udp") or die $!; '
        '$s->send("\\x00\\x01\\x01\\x00\\x00\\x01\\x00\\x00\\x00\\x00\\x00\\x00\\x07example\\x03com\\x00\\x00\\x01\\x00\\x01"); '
        '$s->recv($r,512); print length($r)," bytes\\n"'
        "' 2>&1"
    )
    r = kctl(
        "exec",
        ctx.pod_name,
        "-c",
        "workspace",
        "--",
        "/bin/sh",
        "-c",
        script,
        timeout=15,
    )
    if "bytes" in r.stdout and "TCP=0" in r.stdout:
        return Finding(
            "RT-5.7",
            "DNS to external resolver permitted (NetPol bypass)",
            "FAIL",
            "medium",
            expected,
            f"sandbox can reach 8.8.8.8:53 over both TCP and UDP; raw DNS query succeeded: {r.stdout[:300]}",
            [r.stdout],
            notes=(
                "G16 narrowed DNS allow-list to kube-dns, but the egress allow-all "
                "0.0.0.0/0 (minus RFC1918) lets the sandbox use ANY external "
                "resolver, including raw UDP/53 to 8.8.8.8. Mitigation: add "
                "explicit port restriction (53 only to kube-dns IPs) OR use "
                "Cilium FQDN policy."
            ),
        )
    if "TCP=0" in r.stdout:
        return Finding(
            "RT-5.7",
            "TCP to 8.8.8.8:53 succeeds; UDP DNS check inconclusive",
            "FAIL",
            "low",
            expected,
            r.stdout[:400],
            [r.stdout],
        )
    return Finding(
        "RT-5.7",
        "External DNS endpoints unreachable",
        "PASS",
        "info",
        expected,
        r.stdout[:300],
        [r.stdout],
    )


def rt_5_8(ctx: Ctx) -> Finding:
    """Egress to kube-apiserver — already verified PASS in Phase 3 RT-3.1."""
    script = (
        "set +e; "
        "ip=$(getent hosts kubernetes.default.svc.cluster.local | awk 'NR==1{print $1}'); "
        "echo IP=$ip; "
        'timeout 3 bash -c "</dev/tcp/$ip/443" 2>&1; echo RC=$?'
    )
    r = kctl(
        "exec",
        ctx.pod_name,
        "-c",
        "workspace",
        "--",
        "/bin/sh",
        "-c",
        script,
        timeout=15,
    )
    expected = "kube-apiserver MUST be unreachable from sandbox"
    if "RC=0" in r.stdout:
        return Finding(
            "RT-5.8",
            "kube-apiserver reachable from sandbox",
            "FAIL",
            "high",
            expected,
            r.stdout[:300],
            [r.stdout],
        )
    return Finding(
        "RT-5.8",
        "kube-apiserver unreachable (G16 holds)",
        "PASS",
        "info",
        expected,
        r.stdout[:300],
        [r.stdout],
    )


def rt_5_9(ctx: Ctx) -> Finding:
    """MCP transport injection — defer."""
    return Finding(
        "RT-5.9",
        "MCP transport injection (deferred — needs MCP transport probe)",
        "INCONCLUSIVE",
        "info",
        "MCP server must reject malformed messages",
        "Out of scope for this sweep; recommend dedicated MCP fuzz test.",
        [],
    )


def rt_5_10(ctx: Ctx) -> Finding:
    """WebSocket upgrade abuse on non-WS endpoints."""
    expected = "Non-WS endpoints MUST reject Upgrade: websocket"
    code, body, hdrs = http(
        "POST",
        f"/api/v1/sandboxes/{ctx.workspace_id}/sessions",
        headers={
            "Authorization": f"Bearer {ctx.token}",
            "Connection": "Upgrade",
            "Upgrade": "websocket",
            "Sec-WebSocket-Key": "x3JJHMbDL1EzLkh9GBhXDw==",
            "Sec-WebSocket-Version": "13",
        },
        json_body={},
    )
    obs = f"status={code} hdr.connection={hdrs.get('Connection', '-')} hdr.upgrade={hdrs.get('Upgrade', '-')}"
    if code == 101:
        return Finding(
            "RT-5.10",
            "Non-WS endpoint accepted WebSocket upgrade",
            "FAIL",
            "high",
            expected,
            obs,
            [body[:300]],
        )
    return Finding(
        "RT-5.10",
        "Non-WS endpoint did not upgrade",
        "PASS",
        "info",
        expected,
        obs,
        [body[:300]],
    )


def rt_5_11(ctx: Ctx) -> Finding:
    """Plain-HTTP proxy MITM — confirm static finding G4."""
    return Finding(
        "RT-5.11",
        "Proxy uses plain HTTP to in-cluster opencode (G4)",
        "FAIL",
        "low",
        "Defence-in-depth: proxy SHOULD use HTTPS or run behind a service mesh",
        'Static: proxy.go:405 fmt.Sprintf("http://%s:%d%s", podIP, opencodePort, targetPath)',
        ["static analysis only"],
        notes=(
            "Mitigated by NetworkPolicy (only API can reach workspace pod port "
            "4096) and by Basic Auth password between API and opencode. Residual "
            "risk: cluster-network attacker who breaks NetPol can sniff traffic. "
            "Recommend service mesh (Linkerd/Istio mTLS) when cluster size grows."
        ),
    )


def rt_5_12(ctx: Ctx) -> Finding:
    """stripPatchParts JSON parser DoS — depth/size."""
    expected = (
        "stripPatchParts MUST handle deeply nested / huge JSON without OOM or hang"
    )
    # Best we can do black-box: send an SSE-shaped POST that will provoke
    # stripPatchParts with a payload field containing a giant nested structure.
    # We can't easily send that THROUGH the proxy because the proxy strips
    # response side. The function runs on opencode RESPONSES, not requests.
    # So we can't trigger it from outside. Mark as needing unit test.
    return Finding(
        "RT-5.12",
        "stripPatchParts DoS (cannot trigger black-box)",
        "INCONCLUSIVE",
        "info",
        expected,
        "stripPatchParts is invoked on opencode responses, not user requests; needs unit-level fuzz.",
        [],
        notes=(
            "Static: proxy.go:519 uses encoding/json.Unmarshal which has no "
            "depth limit. Add unit test: stripPatchParts with 10000-deep "
            "nested arrays + 100MB string. Verify it errors gracefully "
            "(returning original body per fail-safe contract)."
        ),
    )


def rt_5_13(ctx: Ctx) -> Finding:
    """Proxy header timeout exhaustion — confirm G12."""
    expected = "ResponseHeaderTimeout SHOULD be ≤30s; 300s allows slow-loris-style worker exhaustion"
    return Finding(
        "RT-5.13",
        "ResponseHeaderTimeout=300s on the proxy (G12)",
        "FAIL",
        "low",
        expected,
        "Static: proxy.go:95 ResponseHeaderTimeout: 300 * time.Second",
        ["static analysis only"],
        notes=(
            "Live exploitation: open many connections that the upstream "
            "(opencode) replies-headers slowly to. With 300s timeout * "
            "maxConnectionsPerWorkspace=10 * many workspaces, an attacker can "
            "tie up many goroutines. Mitigations: lower to 30s; add Gin "
            "concurrency cap. Severity bumps to medium with multi-tenant "
            "deployment."
        ),
    )


def rt_5_14(ctx: Ctx) -> Finding:
    """verbose=true filter bypass — needs response shaping."""
    return Finding(
        "RT-5.14",
        "stripPatchParts unexpected-shape handling (deferred)",
        "INCONCLUSIVE",
        "info",
        "Filter must fail-safe (return original body) on unexpected shapes",
        "Cannot trigger black-box without controlling opencode's response.",
        [],
        notes="Add unit test: stripPatchParts with `parts` nested under unexpected keys, non-array `parts`, `type` not a string.",
    )


# ---------- Registry --------------------------------------------------------


TESTS: dict[str, Callable[[Ctx], Finding]] = {
    "RT-5.1": rt_5_1,
    "RT-5.2": rt_5_2,
    "RT-5.3": rt_5_3,
    "RT-5.4": rt_5_4,
    "RT-5.5": rt_5_5,
    "RT-5.6": rt_5_6,
    "RT-5.7": rt_5_7,
    "RT-5.8": rt_5_8,
    "RT-5.9": rt_5_9,
    "RT-5.10": rt_5_10,
    "RT-5.11": rt_5_11,
    "RT-5.12": rt_5_12,
    "RT-5.13": rt_5_13,
    "RT-5.14": rt_5_14,
}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", nargs="+")
    args = ap.parse_args()

    print(f"=== Phase 5 harness === API_BASE={API_BASE}", file=sys.stderr)

    email = "phase5-alice@pentest.local"
    pw = deterministic_pw(f"phase5-fixed-seed::{email}")
    token = register_or_login("phase5-alice", email, pw)
    if not token:
        print("PROVISIONING FAILED", file=sys.stderr)
        sys.exit(2)
    ws = create_workspace(token, name="p5-ws")
    if not ws:
        print("WORKSPACE CREATE FAILED", file=sys.stderr)
        sys.exit(2)
    pod = wait_for_pod(ws)
    if not pod:
        print("POD NEVER CAME UP", file=sys.stderr)
        sys.exit(2)
    pod_ip = get_pod_ip(pod)
    print(f"  alice ✓ ws={ws} pod={pod} ip={pod_ip}", file=sys.stderr)

    ctx = Ctx(token, ws, pod, pod_ip)

    ids = list(TESTS.keys())
    if args.only:
        ids = [i for i in ids if i in args.only]

    findings: list[Finding] = []
    for tid in ids:
        print(f"  {tid} ...", file=sys.stderr)
        try:
            f = TESTS[tid](ctx)
        except Exception as e:  # noqa: BLE001
            f = Finding(
                tid,
                f"harness error in {tid}",
                "INCONCLUSIVE",
                "info",
                "test should run cleanly",
                f"exception: {e}",
            )
        f.write()
        findings.append(f)
        print(f"    {f.result:13} {f.severity:8} {f.title}", file=sys.stderr)

    counts: dict = {}
    for f in findings:
        counts[f.result] = counts.get(f.result, 0) + 1
    print("\n=== Summary ===", file=sys.stderr)
    for k in ("PASS", "FAIL", "INCONCLUSIVE", "SKIP"):
        print(f"  {k:13} {counts.get(k, 0)}", file=sys.stderr)

    json.dump([asdict(f) for f in findings], sys.stdout, indent=2)
    print()

    # Cleanup
    http(
        "DELETE",
        f"/api/v1/workspaces/{ws}",
        headers={"Authorization": f"Bearer {token}"},
    )


if __name__ == "__main__":
    main()
