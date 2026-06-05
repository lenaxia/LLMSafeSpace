# US-5.4: Create Helm Chart

**Epic:** 5 - Security Hardening
**Priority:** High

## User Story

As a platform operator, I want a Helm chart to deploy LLMSafeSpace, so that I can install and configure the platform on any Kubernetes cluster.

## Acceptance Criteria

- [ ] Chart deploys API server, controller, CRDs
- [ ] Configurable: LLM API domain allowlist, resource quotas, security policies
- [ ] Kyverno policies optional (with warning if disabled)
- [ ] Network policies per security level configurable
- [ ] RBAC, ServiceAccounts, leader election configured

## Technical Details

**New directory:** `charts/llmsafespace/`

```
charts/llmsafespace/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── api-deployment.yaml
│   ├── controller-deployment.yaml
│   ├── crds/
│   │   ├── workspace-crd.yaml
│   │   └── sandbox-crd.yaml
│   ├── rbac.yaml
│   ├── networkpolicy.yaml
│   ├── kyverno/
│   │   └── enforce-sandbox-pod-security.yaml
│   └── configmap.yaml
```

## Design Reference

Section 9.7 (Network Policy), 9.6 (Kyverno), 14 (Roadmap)

## Effort

Large (6-8 hours)
