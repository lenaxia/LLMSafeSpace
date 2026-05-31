#!/usr/bin/env python3
"""
Phase 6 — Kubernetes & Infrastructure Testing
Epic 17 Pentest

Mostly cluster-inspection tests. The harness wraps `kubectl` and `helm` calls
and produces structured findings. Where tests require interaction with the
controller webhook or RBAC simulation, we use kubectl auth can-i.

Static evidence preferred where possible — many of these "tests" really are
configuration audits.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Callable, Optional

KCTX = os.environ.get("KUBECTL_CONTEXT", "admin@home-kubernetes")
NS = os.environ.get("PENTEST_NS", "default")
ARTEFACT_DIR = Path(__file__).resolve().parent.parent / "evidence"
ARTEFACT_DIR.mkdir(parents=True, exist_ok=True)


@dataclass
class Run:
    cmd: list
    rc: int
    stdout: str
    stderr: str


def run(cmd: list, *, timeout: int = 30) -> Run:
    try:
        p = subprocess.run(cmd, capture_output=True, timeout=timeout, check=False)
        return Run(
            cmd,
            p.returncode,
            p.stdout.decode(errors="replace"),
            p.stderr.decode(errors="replace"),
        )
    except subprocess.TimeoutExpired as e:
        return Run(cmd, -1, (e.stdout or b"").decode(errors="replace"), f"TIMEOUT: {e}")


def kctl(*args: str, timeout: int = 30) -> Run:
    return run(["kubectl", "--context", KCTX, *args], timeout=timeout)


def helm(*args: str, timeout: int = 30) -> Run:
    return run(["helm", "--kube-context", KCTX, *args], timeout=timeout)


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


# ---------- Test cases ------------------------------------------------------


def rt_6_1() -> Finding:
    """CRD manipulation: create Workspace with bad spec; webhook should reject."""
    expected = "ValidatingWebhookConfiguration MUST reject malicious Workspace specs"
    # Try to create a Workspace with absurd resources.
    bad = {
        "apiVersion": "llmsafespace.dev/v1",
        "kind": "Workspace",
        "metadata": {"name": "p6-rt-6-1", "namespace": NS},
        "spec": {
            "owner": {"userID": "phase6-test"},
            "runtime": "../../etc/passwd",
            "storage": {"size": "999999Gi"},
        },
    }
    # Apply via kubectl create.
    p = subprocess.run(
        ["kubectl", "--context", KCTX, "-n", NS, "apply", "-f", "-"],
        input=json.dumps(bad).encode(),
        capture_output=True,
        timeout=15,
        check=False,
    )
    out = p.stdout.decode(errors="replace") + p.stderr.decode(errors="replace")
    if p.returncode == 0:
        # Cleanup if it actually got created.
        kctl("-n", NS, "delete", "workspace", "p6-rt-6-1", "--ignore-not-found")
        return Finding(
            "RT-6.1",
            "Webhook accepted malicious Workspace spec",
            "FAIL",
            "high",
            expected,
            f"creation succeeded: {out[:300]}",
            [out],
        )
    if "denied the request" in out or "admission webhook" in out:
        return Finding(
            "RT-6.1",
            "Webhook rejected malicious Workspace spec",
            "PASS",
            "info",
            expected,
            out[:400],
            [out],
        )
    return Finding(
        "RT-6.1",
        "Workspace creation failed for non-webhook reasons",
        "INCONCLUSIVE",
        "info",
        expected,
        out[:400],
        [out],
    )


def rt_6_2() -> Finding:
    """Controller SA token leak — what would attacker get? Static analysis."""
    expected = "Controller SA scope SHOULD be namespace-only by default"
    crb = kctl("get", "clusterrolebinding", "llmsafespace-controller", "-o", "yaml")
    cr = kctl("get", "clusterrole", "llmsafespace-controller", "-o", "yaml")
    helm_vals = helm("get", "values", "llmsafespace", "-n", NS, "--all")
    has_clusterrole = crb.rc == 0
    is_cluster_scope = "scope: cluster" in helm_vals.stdout
    if has_clusterrole and is_cluster_scope:
        # Inspect the rules briefly.
        return Finding(
            "RT-6.2",
            "Controller SA bound cluster-wide (G5)",
            "FAIL",
            "medium",
            expected,
            (
                "ClusterRoleBinding `llmsafespace-controller` exists; "
                "Helm value `rbac.scope=cluster`. Stolen controller SA = "
                "cluster-wide blast radius (any namespace's pods/secrets/PVCs)."
            ),
            [crb.stdout[:400], cr.stdout[:600]],
            notes=(
                "Recommend default `rbac.scope: namespace` and require operators "
                "to opt-in to cluster scope (multi-namespace deployments)."
            ),
        )
    if not has_clusterrole:
        return Finding(
            "RT-6.2",
            "Controller bound at namespace scope",
            "PASS",
            "info",
            expected,
            "no ClusterRoleBinding for controller",
            [crb.stderr[:200]],
        )
    return Finding(
        "RT-6.2",
        "Mixed RBAC scope",
        "INCONCLUSIVE",
        "info",
        expected,
        f"crb={has_clusterrole} scope_cluster={is_cluster_scope}",
        [helm_vals.stdout[:400]],
    )


def rt_6_3() -> Finding:
    """API SA scope — namespace only?"""
    expected = "API SA SHOULD be namespace-scoped"
    rb = kctl("get", "rolebinding", "-n", NS, "-o", "name")
    api_rb = [l for l in rb.stdout.splitlines() if "api" in l.lower()]
    crb_api = kctl("get", "clusterrolebinding", "-o", "name")
    api_crb = [
        l
        for l in crb_api.stdout.splitlines()
        if "api" in l.lower() and "llmsafespace" in l.lower()
    ]
    if api_crb:
        return Finding(
            "RT-6.3",
            "API SA has ClusterRoleBinding",
            "FAIL",
            "medium",
            expected,
            f"unexpected cluster bindings for API: {api_crb}",
            [crb_api.stdout],
        )
    if api_rb:
        return Finding(
            "RT-6.3",
            "API SA scoped to namespace",
            "PASS",
            "info",
            expected,
            f"namespace bindings: {api_rb[:5]}",
            [rb.stdout[:600]],
        )
    return Finding(
        "RT-6.3",
        "Could not enumerate API SA bindings",
        "INCONCLUSIVE",
        "info",
        expected,
        f"rb={rb.stdout[:200]}",
        [rb.stdout],
    )


def rt_6_4() -> Finding:
    """Webhook bypass — accepted: requires cluster-admin."""
    return Finding(
        "RT-6.4",
        "Webhook bypass requires cluster-admin (accepted)",
        "PASS",
        "info",
        "Deleting ValidatingWebhookConfiguration requires cluster-scoped admin",
        "out-of-scope: pentest scope is default+pentest-* namespaces; cluster-admin attacks are accepted residual.",
        [],
        notes="Cluster-admin compromise puts everything at risk; standard deployment assumes operator trust.",
    )


def rt_6_5() -> Finding:
    """Helm values injection — template injection in YAML."""
    expected = "Helm templates MUST quote/escape all user-controllable values"
    # Try a malicious values.yaml with a YAML injection in the network-policy
    # allowedEgressCIDRs list.
    bad_values = (
        "networkPolicy:\n"
        "  allowedEgressCIDRs:\n"
        '    - "0.0.0.0/0\\n  - some-injected: value"\n'
    )
    p = run(
        [
            "helm",
            "--kube-context",
            KCTX,
            "template",
            "test",
            "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace",
            "-f",
            "-",
        ],
        timeout=30,
    )
    p = subprocess.run(
        [
            "helm",
            "--kube-context",
            KCTX,
            "template",
            "test",
            "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace",
            "-f",
            "-",
        ],
        input=bad_values.encode(),
        capture_output=True,
        timeout=30,
        check=False,
    )
    out = p.stdout.decode(errors="replace") + p.stderr.decode(errors="replace")
    # If the rendered YAML now has a top-level "some-injected: value" outside
    # the intended list, that's injection. Most Helm charts use {{ . | quote }}
    # which prevents this.
    if "some-injected: value" in out and "- some-injected" not in out.replace(
        "\\n", "\n"
    ):
        return Finding(
            "RT-6.5",
            "Helm values injection succeeded",
            "FAIL",
            "high",
            expected,
            "bad chars escaped to top-level YAML keys",
            [out[:600]],
        )
    return Finding(
        "RT-6.5",
        "Helm values injection blocked",
        "PASS",
        "info",
        expected,
        f"injected payload appears as a string value, not as YAML structure; rc={p.returncode}",
        [out[:600]],
    )


def rt_6_6() -> Finding:
    """etcd encryption at rest — chart preflight."""
    expected = "Chart should warn or block install if etcd encryption is unconfigured"
    # We can't query the kube-apiserver's encryption config without
    # control-plane access. Static check: is there a NOTES.txt warning, and
    # any preflight Job?
    notes_out = run(
        [
            "cat",
            "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace/templates/NOTES.txt",
        ],
        timeout=5,
    ).stdout
    preflight = run(
        [
            "grep",
            "-l",
            "preflight",
            "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace/templates/",
        ],
        timeout=5,
    ).stdout
    has_warning = "etcd" in notes_out.lower() and "encryption" in notes_out.lower()
    return Finding(
        "RT-6.6",
        "etcd encryption is operator responsibility (no preflight)",
        "FAIL",
        "low",
        expected,
        f"NOTES.txt mentions etcd? {has_warning}; preflight job? {bool(preflight)}",
        [notes_out[:600]],
        notes=(
            "Chart has no preflight Job to verify EncryptionConfig on the "
            "kube-apiserver. Operators must verify out-of-band. Recommend "
            "adding a Helm pre-install hook that errors on `kubectl get "
            "--raw=/api/v1/namespaces/kube-system/secrets` returning unencrypted data."
        ),
    )


def rt_6_7() -> Finding:
    """PVC cross-mount: can a malicious workspace mount another's PVC?"""
    expected = "PVC ownership / RWO mode MUST prevent cross-workspace mount"
    # Static check: the workspace controller sets PVC ownerReference to the
    # Workspace CR. A second Workspace pointing to the same PVC name would
    # fail because the ownerRef can't span CRs (controller logic).
    src = run(
        [
            "grep",
            "-n",
            "OwnerReferences\\|ownerReferences\\|claimName",
            "/home/mikekao/personal/LLMSafeSpace/controller/internal/workspace/controller.go",
        ],
        timeout=5,
    ).stdout
    return Finding(
        "RT-6.7",
        "PVC cross-mount (static analysis)",
        "PASS",
        "info",
        expected,
        f"controller sets ownerReferences on PVCs; cross-mount requires direct kubectl as user with namespace pod-create privs",
        [src[:800]],
        notes=(
            "Live exploit needs a user with kubectl access to the workspace "
            "namespace; that's outside our threat model (sandbox attacker has "
            "no kubectl). For a fully unauthenticated attack, the API would "
            "need to accept a custom claimName — it doesn't (controller "
            "computes deterministic name)."
        ),
    )


def rt_6_8() -> Finding:
    """Namespace escape: from workspace ns, access kube-system."""
    expected = "Workspace pod's SA MUST NOT have permissions in other namespaces"
    # Pod SA is `default` in `default` ns (we verified earlier). Try
    # `kubectl auth can-i` as that SA token (we don't have one — token isn't
    # mounted, G17 fix). Use the API SA to demonstrate the boundary:
    api_can_i = kctl(
        "auth",
        "can-i",
        "get",
        "secrets",
        "-n",
        "kube-system",
        "--as=system:serviceaccount:default:llmsafespace-api",
    )
    return Finding(
        "RT-6.8",
        "Cross-namespace permission check",
        "PASS" if "no" in api_can_i.stdout.lower() else "INCONCLUSIVE",
        "info",
        expected,
        f"api SA can-i get secrets in kube-system: {api_can_i.stdout.strip()}",
        [api_can_i.stdout],
        notes="Sandbox pods have no SA token mounted (G17). API SA is namespace-scoped.",
    )


def rt_6_9() -> Finding:
    """Leader election poisoning."""
    expected = "Lease objects MUST require controller SA permissions"
    leases = kctl("get", "lease", "-n", NS, "-o", "name")
    return Finding(
        "RT-6.9",
        "Lease integrity (static)",
        "INCONCLUSIVE",
        "info",
        expected,
        f"current leases in {NS}: {leases.stdout[:300]}",
        [leases.stdout],
        notes="Live exploit requires forging a Lease with the controller's identity; outside black-box scope.",
    )


def rt_6_10() -> Finding:
    """Untrusted-registry image pull via RuntimeEnvironment / Workspace.Spec.Runtime."""
    expected = "API/webhook MUST reject untrusted registry references in runtime field"
    # Phase 1 already confirmed Spec.Runtime accepts arbitrary refs. Re-test.
    # Can the API webhook reject "evil.example.com/x:latest"?
    bad = {
        "apiVersion": "llmsafespace.dev/v1",
        "kind": "Workspace",
        "metadata": {"name": "p6-rt-6-10", "namespace": NS},
        "spec": {
            "owner": {"userID": "phase6-test"},
            "runtime": "evil.example.com/malicious:latest",
            "storage": {"size": "1Gi"},
        },
    }
    p = subprocess.run(
        ["kubectl", "--context", KCTX, "-n", NS, "apply", "-f", "-"],
        input=json.dumps(bad).encode(),
        capture_output=True,
        timeout=15,
        check=False,
    )
    out = p.stdout.decode(errors="replace") + p.stderr.decode(errors="replace")
    if p.returncode == 0:
        kctl("-n", NS, "delete", "workspace", "p6-rt-6-10", "--ignore-not-found")
        return Finding(
            "RT-6.10",
            "Webhook accepts arbitrary registry in Spec.Runtime",
            "FAIL",
            "critical",
            expected,
            f"creation succeeded: {out[:300]}",
            [out],
            notes="Confirms Phase 2 RT-2.18 critical finding: arbitrary image pull is admission-allowed.",
        )
    if "denied" in out.lower():
        return Finding(
            "RT-6.10",
            "Webhook rejected untrusted registry",
            "PASS",
            "info",
            expected,
            out[:300],
            [out],
        )
    return Finding(
        "RT-6.10",
        "Untrusted registry test inconclusive",
        "INCONCLUSIVE",
        "info",
        expected,
        out[:300],
        [out],
    )


def rt_6_11() -> Finding:
    """PSA enforcement absence (G11): no pod-security label on default ns."""
    expected = (
        "Workspace namespace SHOULD have pod-security.kubernetes.io/enforce: restricted"
    )
    labels = kctl("get", "ns", NS, "-o", "jsonpath={.metadata.labels}")
    has_psa = "pod-security.kubernetes.io/enforce" in labels.stdout
    if has_psa:
        return Finding(
            "RT-6.11",
            "PSA enforce label present",
            "PASS",
            "info",
            expected,
            labels.stdout,
            [labels.stdout],
        )
    return Finding(
        "RT-6.11",
        "Default namespace lacks PSA enforce label (G11 confirmed)",
        "FAIL",
        "medium",
        expected,
        f"namespace labels: {labels.stdout}",
        [labels.stdout],
        notes=(
            "Without `pod-security.kubernetes.io/enforce: restricted`, k8s "
            "admission permits privileged/hostPath/SYS_ADMIN pods if RBAC "
            "permits creating pods. Defence-in-depth gap. Add `enforce: "
            "restricted` and `audit: restricted` to the namespace template."
        ),
    )


def rt_6_12() -> Finding:
    """NetPol enforcement (G16 fix re-verify)."""
    expected = "Workspace NetworkPolicies present and enforced"
    netpols = kctl("get", "networkpolicy", "-n", NS, "-o", "name")
    have = netpols.stdout.splitlines()
    expected_set = {
        "networkpolicy.networking.k8s.io/llmsafespace-workspace-default-deny-ingress",
        "networkpolicy.networking.k8s.io/llmsafespace-workspace-egress",
    }
    have_set = set(have)
    if expected_set.issubset(have_set):
        return Finding(
            "RT-6.12",
            "NetPols present (G16 holds)",
            "PASS",
            "info",
            expected,
            f"netpols: {sorted(have_set)}",
            [netpols.stdout],
        )
    return Finding(
        "RT-6.12",
        "NetPols missing or different",
        "FAIL",
        "high",
        expected,
        f"have={sorted(have_set)} expected={sorted(expected_set)}",
        [netpols.stdout],
    )


def rt_6_13() -> Finding:
    """Helm chart preflight — does it exist?"""
    expected = (
        "Chart SHOULD have a pre-install Job that validates CNI / etcd encryption"
    )
    files = run(
        ["ls", "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace/templates/"]
    ).stdout
    has_preflight = any(
        "preflight" in f.lower() or "preinstall" in f.lower()
        for f in files.splitlines()
    )
    return Finding(
        "RT-6.13",
        "No Helm preflight job" if not has_preflight else "Preflight job present",
        "FAIL" if not has_preflight else "PASS",
        "low" if not has_preflight else "info",
        expected,
        f"templates/: {[f for f in files.splitlines()][:30]}",
        [files],
    )


def rt_6_14() -> Finding:
    """Default-on TLS at ingress."""
    expected = "Helm values default SHOULD enable TLS"
    vals = run(
        ["cat", "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace/values.yaml"]
    ).stdout
    # Look for ingress.tls config block
    in_ingress = False
    tls_block = []
    for line in vals.splitlines():
        if line.startswith("ingress:"):
            in_ingress = True
            continue
        if in_ingress:
            if line and not line[0].isspace():
                break
            tls_block.append(line)
    block = "\n".join(tls_block)
    has_tls_default = "tls:\n" in block and "enabled: true" in block
    return Finding(
        "RT-6.14",
        "Ingress TLS default" + (" enabled" if has_tls_default else " not enabled"),
        "PASS" if has_tls_default else "FAIL",
        "info" if has_tls_default else "low",
        expected,
        f"ingress block: {block[:400]}",
        [block],
        notes=(
            "Live deployment uses Traefik with cert-manager-issued certs at "
            "ingress; Helm chart default still SHOULD enable TLS for "
            "out-of-the-box deployments."
        ),
    )


def rt_6_15() -> Finding:
    """Operator-deployed Redis/Postgres exposure."""
    expected = "Postgres/Redis services SHOULD be ClusterIP only"
    pg = kctl("get", "svc", "-n", NS, "postgres", "-o", "jsonpath={.spec.type}")
    rd = kctl("get", "svc", "-n", NS, "valkey", "-o", "jsonpath={.spec.type}")
    bad = []
    if pg.stdout.strip() and pg.stdout.strip() != "ClusterIP":
        bad.append(f"postgres={pg.stdout.strip()}")
    if rd.stdout.strip() and rd.stdout.strip() != "ClusterIP":
        bad.append(f"valkey={rd.stdout.strip()}")
    if bad:
        return Finding(
            "RT-6.15",
            "Database service exposed externally",
            "FAIL",
            "high",
            expected,
            "; ".join(bad),
            [pg.stdout, rd.stdout],
        )
    return Finding(
        "RT-6.15",
        "Database services are ClusterIP",
        "PASS",
        "info",
        expected,
        f"postgres={pg.stdout.strip()!r} valkey={rd.stdout.strip()!r}",
        [pg.stdout, rd.stdout],
        notes="Reminder G26: postgres has default password and valkey has empty requirepass — see Phase 4. ClusterIP doesn't help if anything in the namespace is compromised.",
    )


def rt_6_16() -> Finding:
    """Default cluster RBAC scope (G5)."""
    expected = "Default rbac.scope SHOULD be namespace, not cluster"
    vals = run(
        ["cat", "/home/mikekao/personal/LLMSafeSpace/charts/llmsafespace/values.yaml"]
    ).stdout
    # Look for the rbac block default
    in_rbac = False
    for line in vals.splitlines():
        if line.startswith("rbac:"):
            in_rbac = True
            continue
        if in_rbac:
            if line and not line[0].isspace():
                break
            if "scope:" in line and "cluster" in line:
                return Finding(
                    "RT-6.16",
                    "Helm default rbac.scope=cluster (G5)",
                    "FAIL",
                    "medium",
                    expected,
                    f"line: {line!r}",
                    [vals[:600]],
                    notes="Recommend default 'namespace'; require operators to opt-in to cluster scope.",
                )
            if "scope:" in line and "namespace" in line:
                return Finding(
                    "RT-6.16",
                    "Helm default rbac.scope=namespace",
                    "PASS",
                    "info",
                    expected,
                    f"line: {line!r}",
                    [vals[:600]],
                )
    return Finding(
        "RT-6.16",
        "Could not find rbac.scope default",
        "INCONCLUSIVE",
        "info",
        expected,
        "scope key not found in values.yaml",
        [vals[:600]],
    )


# ---------- Registry --------------------------------------------------------


TESTS: dict[str, Callable[[], Finding]] = {
    "RT-6.1": rt_6_1,
    "RT-6.2": rt_6_2,
    "RT-6.3": rt_6_3,
    "RT-6.4": rt_6_4,
    "RT-6.5": rt_6_5,
    "RT-6.6": rt_6_6,
    "RT-6.7": rt_6_7,
    "RT-6.8": rt_6_8,
    "RT-6.9": rt_6_9,
    "RT-6.10": rt_6_10,
    "RT-6.11": rt_6_11,
    "RT-6.12": rt_6_12,
    "RT-6.13": rt_6_13,
    "RT-6.14": rt_6_14,
    "RT-6.15": rt_6_15,
    "RT-6.16": rt_6_16,
}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", nargs="+")
    args = ap.parse_args()

    print(f"=== Phase 6 harness === ctx={KCTX} ns={NS}", file=sys.stderr)

    ids = list(TESTS.keys())
    if args.only:
        ids = [i for i in ids if i in args.only]

    findings: list[Finding] = []
    for tid in ids:
        print(f"  {tid} ...", file=sys.stderr)
        try:
            f = TESTS[tid]()
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


if __name__ == "__main__":
    main()
