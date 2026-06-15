# Worklog 0287 — Epic 42: Add AWS as Paid Primary Provider

**Date:** 2026-06-15
**Epic:** 42 (Multi-Cloud Inference Relay)
**PR:** #157
**Scope:** Add AWS as paid primary relay provider with cascading failover

## Summary

Changed the relay fleet model from OCI-primary/GCP-failover to AWS-primary/
OCI-secondary. GCP is dropped from the default fleet (Always Free tier
eliminated — see A12). The operator can still add GCP as a paid provider.

## Changes

### Router cascade logic (`cmd/relay-router/fleet.go`)
- `relayWeight`: AWS=1000, OCI=100, GCP=1 (was OCI=100, GCP=1)
- `SelectRelay`: 3-tier cascade — AWS gets 100% when eligible; OCI gets 100%
  when no AWS eligible; GCP gets traffic only when both AWS+OCI unavailable
- `hasOCI` check guarded by `if !hasAWS` to avoid redundant scan

### CRD types (`pkg/apis/llmsafespace/v1/inferencerelay_types.go`)
- Enum updated: `oci;gcp` → `aws;oci;gcp`
- WireGuard CIDR comment updated: AWS relay is .4
- Provider doc comments updated for 3-provider model

### Tests (`cmd/relay-router/fleet_test.go`)
- `TestSelectRelay_AWSPrimaryWhenAllHealthy` — all 3 healthy → AWS gets 100%
- `TestSelectRelay_OCIPrimaryWhenAWSAbsent` — no AWS → OCI gets 100%
- `TestSelectRelay_OCIFailoverWhenAWSUnhealthy` — AWS unhealthy → OCI
- `TestSelectRelay_OCIFailoverWhenAWSDraining` — AWS draining → OCI
- `TestSelectRelay_OCIFailoverWhenAWS429Draining` — AWS 429-draining → OCI
- `TestSelectRelay_GCPFailoverWhenAWSAndOCIDown` — both down → GCP
- Weight tests updated for all 3 providers

### Design doc
- Updated desired state to reflect AWS-primary model
- Removed GCP from default fleet description
