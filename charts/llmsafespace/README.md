# LLMSafeSpace Helm chart

Kubernetes-first deployment for the LLMSafeSpace control plane: API service,
controller, CRDs, ValidatingWebhookConfiguration, and database migrations.

## Status

- Chart version: 0.1.0
- App version: 0.1.0
- Kubernetes: >= 1.27
- Helm: >= 3.13 (also tested with Helm 4)
- Tested locally with `helm lint` and `helm template`

This chart deploys two Deployments (API, controller), two CRDs, a
validating webhook, RBAC, a ConfigMap-driven config, and a pre-install
migration Job.

It does **not** deploy Postgres, Redis, or cert-manager. See "Prerequisites"
below.

## Prerequisites

### Kubernetes

A cluster running Kubernetes 1.27 or later. The webhook configuration uses
`admissionregistration.k8s.io/v1` and `cert-manager.io/v1`.

### cert-manager

If `webhooks.enabled=true` (the default), [cert-manager](https://cert-manager.io)
must be installed in the cluster. The chart uses `cert-manager.io/v1`
`Issuer` and `Certificate` resources, plus the
`cert-manager.io/inject-ca-from` annotation read by `cainjector`.

Install cert-manager:

```sh
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.0/cert-manager.yaml
kubectl wait --for=condition=Available -n cert-manager deployment/cert-manager-webhook --timeout=120s
```

If you cannot install cert-manager, set `webhooks.enabled=false`. Admission
validation will only be enforced client-side by the API service. Operators
using `kubectl` directly will bypass validation.

### Postgres

The chart does NOT bundle Postgres. Provide an existing Postgres instance
reachable from the cluster. Configure via `postgresql.host`, `postgresql.port`,
`postgresql.database`, `postgresql.user`, plus the password in
`externalSecret.postgresPassword` (or via an existing Secret pointed at by
`externalSecret.existingSecret`).

The migration Job uses `migrate/migrate:v4.17.1` and expects the database
named in `postgresql.database` to **already exist**. The migrations create
schema objects, not the database itself.

For a quick local Postgres in kind:

```sh
helm install pg oci://registry-1.docker.io/bitnamicharts/postgresql \
    --version 13.4.4 \
    --set auth.username=llmsafespace \
    --set auth.password=changeme \
    --set auth.database=llmsafespace \
    -n llmsafespace --create-namespace
# (Or use any other Postgres chart / raw manifests / cloud DB.)
```

### Redis

Same story — provide an existing Redis. Configure `redis.host`, `redis.port`,
and optionally `externalSecret.redisPassword`.

## Quick install

```sh
# 1. Install cert-manager (see above)

# 2. Install LLMSafeSpace
helm install llmsafespace ./charts/llmsafespace \
    -n llmsafespace --create-namespace \
    --set postgresql.host=pg-postgresql \
    --set redis.host=redis-master \
    --set externalSecret.postgresPassword=changeme

# 3. Watch the migration Job complete
kubectl -n llmsafespace get jobs -w

# 4. Wait for both Deployments
kubectl -n llmsafespace rollout status deployment llmsafespace-api
kubectl -n llmsafespace rollout status deployment llmsafespace-controller

# 5. Smoke-test the API
kubectl -n llmsafespace port-forward svc/llmsafespace-api 8080:8080 &
curl http://localhost:8080/livez   # 200 OK
curl http://localhost:8080/readyz  # 200 if DB+Redis healthy, 503 otherwise
```

## Values reference

The chart exposes ~150 documented values. Highlights:

| Key | Default | Purpose |
|-----|---------|---------|
| `api.replicaCount` | `2` | Number of API pods |
| `api.image.repository` | `llmsafespace/api` | API container image |
| `api.config.rateLimiting.enabled` | `true` | API per-user rate limiting |
| `controller.replicaCount` | `1` | Number of controller pods |
| `controller.watchNamespaces` | `""` | Comma-separated list of namespaces to watch (empty = all) |
| `controller.leaderElection.enabled` | `true` | Use leader election for HA controller |
| `crds.install` | `true` | Install CRDs from `crds/` |
| `rbac.create` | `true` | Create (Cluster)Role and (Cluster)RoleBinding |
| `rbac.scope` | `"namespace"` | `"cluster"` or `"namespace"` (defense-in-depth) |
| `webhooks.enabled` | `true` | Deploy ValidatingWebhookConfiguration (requires cert-manager) |
| `webhooks.failurePolicy` | `"Fail"` | Admission failure policy |
| `migrations.enabled` | `true` | Run migrations as pre-install/upgrade Helm hook |
| `externalSecret.create` | `true` | Create the credentials Secret from chart values |
| `externalSecret.existingSecret` | `""` | Reference an existing Secret instead |
| `postgresql.host` / `port` / `database` / `user` / `sslMode` | — | Postgres connection |
| `redis.host` / `port` / `db` | — | Redis connection |

See [`values.yaml`](./values.yaml) for the full list with comments.

## Operational concerns

### Probes

- API `/livez` returns 200 if the process is responding (used for liveness)
- API `/readyz` returns 200 only when both Postgres and Redis are reachable
  (used for readiness; pings have a 2s timeout)
- API `/health` is preserved as a legacy alias for `/livez`
- Controller exposes controller-runtime's `/healthz` and `/readyz`

The probe paths are excluded from auth, logging, and metrics middleware so
kubelet probes don't generate auth errors or pollute Prometheus cardinality.

### CRD upgrades

Helm 3 installs CRDs from `crds/` on first install but **does not upgrade**
them on `helm upgrade`. To pick up CRD changes:

```sh
kubectl apply -f charts/llmsafespace/crds/
helm upgrade llmsafespace ./charts/llmsafespace -n llmsafespace
```

For production safety, set `crds.install=false` and manage CRDs out-of-band.

### Webhook failure mode

`webhooks.failurePolicy` defaults to `Fail`: if the controller webhook is
unavailable, kube-apiserver rejects all CREATE/UPDATE on RuntimeEnvironment.
This is the secure default but means controller downtime blocks runtime
submissions. Set to `Ignore` for availability over security, or `Fail` for
security over availability.

### RBAC scope

`rbac.scope=namespace` (default) gives the controller only namespace-scoped Role on
the release namespace. Combine with `controller.watchNamespaces=<release-ns>`
for tightest isolation. Resources in other namespaces will not be reconciled.

`rbac.scope=cluster` gives the controller cluster-wide permissions.
This is required when `controller.watchNamespaces` is empty (cluster-wide
mode).

### Workspace namespace

By default, workspace CRDs are created in `.Release.Namespace`. Override with
`api.config.kubernetes.namespace` to deploy workspaces elsewhere. RBAC for
the API ServiceAccount is created in that namespace.

## Validating the chart

```sh
make helm-lint                  # syntax check
make helm-template               # render with defaults
make helm-template-debug         # render with full debug output
make helm-install-dry-run        # validate against live cluster
make helm-package                # produce dist/llmsafespace-0.1.0.tgz
```

## Uninstalling

```sh
helm uninstall llmsafespace -n llmsafespace
kubectl -n llmsafespace delete pvc --all   # PVCs are not deleted by Helm
kubectl delete crd workspaces.llmsafespace.dev runtimeenvironments.llmsafespace.dev
```

CRDs are intentionally not deleted by `helm uninstall` (Helm 3 default
behavior) to avoid accidental data loss. Delete them manually if intended.

## Limitations

- No Kyverno policy templates yet (deferred per EVOLUTION-V2.md §9.6)
- No bundled Postgres or Redis sub-charts
- Migrations run with the official `migrate/migrate`` image; the database
  must exist before the chart is installed (`CREATE DATABASE` is not run)
- The API service does not yet support TLS at the API level (use an Ingress
  with TLS termination)

## Monitoring

The chart optionally deploys Grafana dashboards, Prometheus alerting rules,
and ServiceMonitor resources. All are gated by `monitoring.enabled` (off by
default) with independent sub-toggles.

### Prerequisites

- **Grafana** with the [sidecar dashboard
  importer](https://github.com/grafana/helm-charts/tree/main/charts/grafana#sidecar-dashboard-provider)
  (the dashboard ConfigMap uses the `grafana_dashboard: "1"` label)
- **Prometheus Operator** (for `PrometheusRule` and `ServiceMonitor` CRDs)

### Enabling

```sh
helm install llmsafespace ./charts/llmsafespace \
    --set monitoring.enabled=true \
    -n llmsafespace
```

### What is deployed

| Resource | Count | Toggle |
|----------|-------|--------|
| Grafana dashboard ConfigMap | 1 (2 dashboards) | `monitoring.dashboards.enabled` |
| PrometheusRule | 1 (19 alert rules) | `monitoring.prometheusRules.enabled` |
| ServiceMonitor | 2 (API + controller) | `monitoring.serviceMonitors.enabled` |

### Dashboards

- **LLMSafeSpace - Operational**: request overview, connections, workspace
  lifecycle, reconciliation, recovery, agent operations, SSE/relay, billing
  at a glance
- **LLMSafeSpace - Billing & Metering**: inference cost/token breakdown,
  per-user metering (active seconds, CPU, LLM calls), per-workspace resource
  consumption (storage, memory, CPU, proxy bytes)

### Alerting rules

19 alert rules across 4 groups (`llmsafespace.api`,
`llmsafespace.controller`, `llmsafespace.agentd`, `llmsafespace.billing`).
Key alerts:

- API error rate >5% (warning) / >15% (critical)
- API p99 latency >5s
- Workspace creation >120s at p99
- Consecutive workspace failures >3 (critical)
- Agentd startup >60s at p95
- Inference cost rate >$10/hour
- Workspace disk usage >90%

### Controller metrics endpoint

When `monitoring.serviceMonitors.enabled=true`, the controller deployment
automatically overrides `controller.metricsAddr` to `0.0.0.0:8080` so the
ServiceMonitor can reach the metrics endpoint through the Kubernetes
Service. Without this, the default loopback binding (`127.0.0.1:8080`)
rejects connections from other pods.

**Security note:** This exposes controller metrics (including reconciliation
details and workspace phase transitions) to any pod that can reach the
controller Service. For production, consider deploying a `kube-rbac-proxy`
sidecar to authenticate scrapes, or use NetworkPolicy to restrict access to
the controller metrics port.

### API metrics authentication

The API `/metrics` endpoint requires `Authorization: Bearer <token>` when
the `LLMSAFESPACE_METRICS_TOKEN` env var is set. If you use this env var,
configure the ServiceMonitor's bearer token:

```yaml
monitoring:
  enabled: true
  serviceMonitors:
    api:
      bearerTokenSecret:
        name: llmsafespace-metrics-token
        key: token
```

If `LLMSAFESPACE_METRICS_TOKEN` is not set, the endpoint is unauthenticated
and no `bearerTokenSecret` configuration is needed.

### Configuration reference

| Key | Default | Purpose |
|-----|---------|---------|
| `monitoring.enabled` | `false` | Master toggle for all monitoring resources |
| `monitoring.dashboards.enabled` | `true` | Deploy Grafana dashboard ConfigMap |
| `monitoring.dashboards.namespace` | `""` | Override namespace (defaults to release namespace) |
| `monitoring.dashboards.labels` | `{grafana_dashboard: "1"}` | Labels for dashboard ConfigMap |
| `monitoring.prometheusRules.enabled` | `true` | Deploy PrometheusRule alerts |
| `monitoring.prometheusRules.namespace` | `""` | Override namespace |
| `monitoring.prometheusRules.labels` | `{}` | Additional labels (e.g. `role: alert-rules`) |
| `monitoring.serviceMonitors.enabled` | `true` | Deploy ServiceMonitor for API + controller |
| `monitoring.serviceMonitors.namespace` | `""` | Override namespace |
| `monitoring.serviceMonitors.interval` | `30s` | Scrape interval |
| `monitoring.serviceMonitors.scrapeTimeout` | `10s` | Scrape timeout |
| `monitoring.serviceMonitors.api.bearerTokenSecret` | `{}` | Optional bearer token for API metrics auth |
