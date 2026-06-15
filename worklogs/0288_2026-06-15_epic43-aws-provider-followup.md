# Worklog 0288 — Epic 43: AWS Provider Support Follow-up

**Date:** 2026-06-15
**Epic:** 43 (Relay Admin UX)
**PR:** #176
**Scope:** Update Epic 43 admin handler to accept AWS after CRD enum update

## Summary

PR #157 (Epic 42) added `aws` to the InferenceRelay CRD enum (`aws;oci;gcp`).
This PR updates the Epic 43 admin handler and frontend to support AWS as the
paid primary provider.

## Changes

### Backend (`api/internal/handlers/relay_admin.go`)
- Deploy handler: added `aws` case (region us-east-1, cred ref `aws-relay-irwa`)
- New `SaveAWSCreds` endpoint (`POST /admin/relay/aws-creds`)
- `AWSConfigured` field in setup response
- `egressLimitForProvider`: AWS = 100 GB
- `computeCost`: AWS = $7/month when healthy
- Updated stale doc comments

### Frontend (`frontend/src/api/relay.ts`, `RelaySetupWizard.tsx`)
- Added `AWSCredsRequest` type + `saveAWSCreds` API method
- AWS step in wizard (Trust Anchor ID, Profile ID, Role ARN)
- Default deploy: AWS primary + OCI secondary, GCP optional
- Tests updated for 5-step wizard flow

### Tests
- `TestRelayAWSCreds_Create_Success`
- `TestRelayAWSCreds_MissingFields_400` (table-driven)
- `TestRelaySetup_AWSSecretExists_Configured`
- `TestRelayDeploy_AcceptsAWS_Success`
