# Worklog 0068 — Remove Legacy Credential System

**Date:** 2026-05-29
**Session:** Remove legacy `workspace-creds-{id}` credential system

## Summary

Removed the entire legacy credential system that stored LLM provider configs as persistent Kubernetes Secrets (`workspace-creds-{id}`). This system was deprecated in favor of the Epic 10 zero-knowledge secret store, which encrypts credentials with per-user DEKs and only materializes plaintext in pod tmpfs during active sessions.

## What Was Removed

### API Service
- `PUT /api/v1/workspaces/:id/credentials` route
- `DELETE /api/v1/workspaces/:id/credentials` route
- `SetCredentials()` method on workspace service
- `DeleteCredentials()` method on workspace service
- `autoProvisionCredentials()` method
- `CredentialProvisioner` interface + `SetCredentialProvisioner()` setter
- `credProvisionerAdapter` in app.go
- `SetCredentialsRequest` type from `pkg/types/types.go`
- Interface entries in `api/internal/interfaces/interfaces.go`
- Mock methods in `api/internal/mocks/workspace.go`
- All related tests (9 credential tests in workspace_service_test.go, 5 auto-provision tests in workspace_defaults_test.go, cred_provisioner_test.go)

### Controller
- `default_creds.go` — `copyDefaultCredentials()` function
- `default_creds_test.go`
- `secret_watch_test.go` — `mapCredSecretToWorkspaces` tests
- `credentialSecretChanged()` function — detected secret changes and restarted pods
- `checkCredentialState()` function — validated credential secret contents
- `mapCredSecretToWorkspaces()` function — watched for `workspace-creds-*` secret changes
- `hashSecretData()` function
- `AnnotationSuspendOnCredLoss` constant
- `CredentialSecretDataKey` constant
- Legacy credential volume mount in `buildCredentialSetupInit()`
- `Watches(&corev1.Secret{}, ...)` for credential secrets
- Credential-related tests in health_test.go

### Runtime
- Legacy fallback in `entrypoint-common.sh` that copied `/sandbox-cfg/credentials` directly

### Helm Chart
- `templates/default-credentials.yaml` template
- `defaultCredentials` section from `values.yaml`

## What Remains (New System)

The Epic 10 secrets system is the sole credential path:
1. User creates encrypted secrets via `POST /api/v1/secrets`
2. User binds secrets to workspaces via `PUT /api/v1/workspaces/:id/bindings`
3. On activate, API decrypts and creates ephemeral K8s Secret `workspace-secrets-{id}`
4. Init container copies `secrets.json` → `/sandbox-cfg/secrets.json`
5. Entrypoint parses and materializes to correct paths (LLM config, SSH keys, git creds, env vars, files)
6. Ephemeral secret deleted after pod starts

## Tests Run

```
go test -timeout 60s -short -count=1 ./...
# All packages pass (30 ok, 0 FAIL)
```

## Files Modified

- `api/internal/server/router.go` — removed credential routes
- `api/internal/interfaces/interfaces.go` — removed SetCredentials/DeleteCredentials
- `api/internal/mocks/workspace.go` — removed mock methods
- `api/internal/services/workspace/workspace_service.go` — removed 3 functions + interface + field
- `api/internal/services/workspace/workspace_service_test.go` — removed credential tests
- `api/internal/services/workspace/workspace_defaults_test.go` — removed auto-provision tests
- `api/internal/app/app.go` — removed credProvisionerAdapter + wiring
- `api/internal/app/cred_provisioner_test.go` — deleted
- `pkg/types/types.go` — removed SetCredentialsRequest
- `controller/internal/workspace/controller.go` — removed 5 functions + credential volume + watcher
- `controller/internal/workspace/constants.go` — removed 2 constants
- `controller/internal/workspace/default_creds.go` — deleted
- `controller/internal/workspace/default_creds_test.go` — deleted
- `controller/internal/workspace/secret_watch_test.go` — deleted
- `controller/internal/workspace/health_test.go` — removed credential tests, updated init script test
- `runtimes/base/tools/entrypoints/entrypoint-common.sh` — removed legacy fallback
- `charts/llmsafespace/templates/default-credentials.yaml` — deleted
- `charts/llmsafespace/values.yaml` — removed defaultCredentials section
