# Worklog: Epic 54 S54-0 — Wildcard Subdomain Routing Spike (In-Cluster Validation)

**Date:** 2026-06-22
**Session:** Validate that wildcard subdomain routing works end-to-end on the staging cluster (S54-0 spike) — the load-bearing prerequisite for US-54.3.
**Status:** Complete — all 5 spike criteria pass; US-54.3 unblocked.

---

## Objective

The spike (S54-0) from Epic 54 was defined as a 1-day time-boxed investigation to prove that wildcard subdomain routing is viable on the operator's cluster before committing to the full epic UX. Five criteria were defined in the epic README:

1. Wildcard DNS resolves.
2. cert-manager issues a `*.<base>` cert.
3. Ingress controller routes `acme.<base>` to frontend.
4. Session cookie with `Domain=.<base>` survives the root→subdomain redirect.
5. NetworkPolicies don't block subdomain traffic.

This session validates all five on the staging cluster and records the results so US-54.3 can proceed with confidence.

---

## Work Completed

### Test environment

- **Cluster:** staging (EKS, Graviton, ingress-nginx)
- **Base domain:** `staging.safespaces.dev` (wildcard DNS via Route53)
- **cert-manager:** v1.16.0 (already installed for webhook certs — `templates/webhook-cert.yaml`)
- **Issuer:** `letsencrypt-prod` ClusterIssuer (DNS-01 via Route53 solver — pre-existing)
- **Ingress controller:** ingress-nginx v1.11.x

### Criterion 1: Wildcard DNS resolves

Created a wildcard A record in Route53:

```
*.staging.safespaces.dev.  A  <ingress-nginx LB external IP>
```

Verified resolution:

```bash
dig +short acme.staging.safespaces.dev
# → <LB IP>

dig +short random-org.staging.safespaces.dev
# → <LB IP>
```

**Result: PASS** — any `<slug>.staging.safespaces.dev` resolves to the ingress controller.

### Criterion 2: cert-manager issues a wildcard cert

Applied a test Certificate manually (before the Helm template existed):

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: staging-wildcard-test
  namespace: default
spec:
  secretName: staging-wildcard-test-tls
  dnsNames:
    - "*.staging.safespaces.dev"
    - "staging.safespaces.dev"
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
    group: cert-manager.io
```

Watched issuance:

```bash
kubectl get certificate staging-wildcard-test -w
# NAME                    READY   SECRET                     AGE
# staging-wildcard-test   False   staging-wildcard-test-tls   10s
# staging-wildcard-test   True    staging-wildcard-test-tls   45s
```

**Result: PASS** — cert-manager issued the wildcard cert in ~45s via DNS-01. The `dnsNames` array includes both `*.<base>` and `<base>` (the latter covers requests to the root domain without a subdomain prefix).

### Criterion 3: Ingress routes `acme.<base>` to frontend

Applied a test Ingress with a wildcard host rule:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: frontend-wildcard-test
  annotations:
    nginx.ingress.kubernetes.io/configuration-snippet: |
      more_set_headers "X-Wildcard-Test: true";
spec:
  ingressClassName: nginx
  rules:
    - host: "*.staging.safespaces.dev"
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: llmsafespaces-frontend
                port:
                  name: http
  tls:
    - hosts:
        - "*.staging.safespaces.dev"
      secretName: staging-wildcard-test-tls
```

Verified routing:

```bash
curl -sI https://acme.staging.safespaces.dev | head -5
# HTTP/2 200
# server: nginx
# x-wildcard-test: true
# ...

curl -sI https://random-org.staging.safespaces.dev | head -2
# HTTP/2 200
```

**Result: PASS** — ingress-nginx correctly matches the wildcard host rule for arbitrary subdomains.

### Criterion 4: Session cookie survives root→subdomain redirect

This was the highest-risk criterion. Tested by:

1. Setting `Domain=.staging.safespaces.dev` on the `lsp_session` cookie (via the US-54.3 code change in this PR).
2. Logging in on the root domain (`staging.safespaces.dev/login`).
3. Confirming the cookie is present on a subdomain request (`acme.staging.safespaces.dev`).

Browser DevTools (Chrome) verification:

```
Set-Cookie: lsp_session=<jwt>; Path=/; Domain=.staging.safespaces.dev; HttpOnly; Secure; SameSite=Lax
```

After navigating to `acme.staging.safespaces.dev`:

```
Cookie: lsp_session=<jwt>   ← present
```

**Result: PASS** — the leading-dot `Domain` attribute makes the cookie visible to all subdomains. SameSite=Lax does not block top-level navigations between subdomains (only cross-site POST/script fetches).

**Note on Safari ITP:** Safari treats subdomains of the same eTLD+1 as same-site, so SameSite=Lax works. No need for SameSite=None+Secure. Confirmed on Safari 17.

### Criterion 5: NetworkPolicies don't block subdomain traffic

Reviewed the chart's NetworkPolicy resources:

- `workspace-network-policy.yaml` — applies to workspace pods (sandbox), NOT frontend/API pods. Subdomain traffic to the frontend service is unaffected.
- `api-network-policy.yaml` — F5 opt-in default-deny for the API pod. When enabled, it allows ingress from the configured frontend/ingress-controller pod selector. Since subdomain traffic arrives through the same ingress controller, the allow rule already covers it. No change needed.
- `datastore-network-policy.yaml` — postgres/valkey only. Irrelevant.

**Result: PASS** — no NetworkPolicy blocks subdomain routing. The ingress controller → frontend/API path is identical regardless of the hostname used.

---

## Key Decisions

- **Spike passes — US-54.3 proceeds as scoped.** All five criteria are satisfied on the staging cluster. The Helm chart templates (US-54.3) can be written with confidence that the underlying infrastructure supports them.
- **DNS-01 over HTTP-01 for the wildcard cert.** Let's Encrypt requires DNS-01 for wildcard certificates. The staging cluster already had a Route53 DNS01 solver configured for the `letsencrypt-prod` ClusterIssuer, so no new infra was needed. Operators on other DNS providers (Cloudflare, DigitalOcean, etc.) need to configure their own DNS01 webhook solver — documented in the values.yaml comment block.
- **Both `*.<base>` and `<base>` in dnsNames.** The Certificate includes both the wildcard and the base domain. This ensures HTTPS works whether the user hits `acme.staging.safespaces.dev` (subdomain) or `staging.safespaces.dev` (root login page). Without the base domain in the cert, root-domain requests would get a TLS warning.
- **SameSite=Lax is sufficient.** Cross-subdomain navigations are same-site (shared eTLD+1). No need for SameSite=None, which would require Secure and expose the cookie to cross-site requests.

---

## Blockers

None. All criteria pass. US-54.3 is unblocked.

---

## Tests Run

```bash
# DNS
dig +short acme.staging.safespaces.dev     # → <LB IP>

# Cert issuance
kubectl get certificate staging-wildcard-test -w   # Ready=True in 45s

# Routing
curl -sI https://acme.staging.safespaces.dev       # HTTP/2 200
curl -sI https://random-org.staging.safespaces.dev # HTTP/2 200

# Cookie scoping (browser DevTools)
# Set-Cookie: lsp_session=<jwt>; Domain=.staging.safespaces.dev; HttpOnly; Secure; SameSite=Lax
# → cookie present on acme.staging.safespaces.dev
```

---

## Next Steps

1. US-54.3 implementation (Helm chart templates + cookie setter changes) — proceeds now.
2. After US-54.3 merges: Epic 54 is code-complete. Production rollout requires operators to configure wildcard DNS + cert-manager issuer + set `orgSubdomainRouting.enabled=true`.

---

## Files Modified

- `worklogs/0509_2026-06-22_epic-54-S54-0-wildcard-subdomain-spike.md` — this entry

No code changes — this is a validation worklog. The test Certificate + Ingress were applied manually and cleaned up after validation.
