// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package chart_test

// Helm chart rendering tests for Epic 17 G16 remediation.
//
// These tests run `helm template` as a subprocess (the same command the
// Makefile target uses and the same code path operators run during
// `helm install`). They assert structural invariants about the rendered
// manifests:
//
//   - A default-deny ingress NetworkPolicy is rendered for the workspace
//     namespace.
//   - A workspace egress allow-list NetworkPolicy is rendered with at
//     least the operator-supplied LLM/DNS allowances.
//   - The NetworkPolicy resources are gated on values.networkPolicy.enabled
//     so operators with their own policy controllers can opt out.
//   - The default value of networkPolicy.enabled is true (Epic 17 requires
//     secure-by-default).
//   - The cluster default of rbac.scope is "namespace" (G5 follow-on);
//     defer that to a later remediation, but assert the file's presence.
//
// The tests are designed to fail clearly if any contract bit drifts. They
// don't assert exact YAML content because Helm renders fields in
// non-deterministic order; they parse the output as YAML documents and
// query by kind/name.
//
// To run:
//
//	go test ./charts/llmsafespaces/...
//
// helm must be on $PATH. The test skips otherwise.

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func chartDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Dir(thisFile)
}

func helmTemplate(t *testing.T, valuesYAML string) []map[string]any {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH; skipping chart render test")
	}

	args := []string{"template", "test-release", chartDir(t), "-n", "test-ns"}
	if valuesYAML != "" {
		dir := t.TempDir()
		valuesPath := filepath.Join(dir, "values.yaml")
		require.NoError(t, writeFile(valuesPath, valuesYAML))
		args = append(args, "-f", valuesPath)
	}
	cmd := exec.Command("helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "helm template failed: %s", stderr.String())

	docs := splitYAMLDocs(stdout.Bytes())
	parsed := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		if len(bytes.TrimSpace(d)) == 0 {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal(d, &m); err != nil {
			t.Logf("skipping unparseable doc: %v\n%s", err, string(d))
			continue
		}
		if m == nil {
			continue
		}
		parsed = append(parsed, m)
	}
	return parsed
}

func splitYAMLDocs(b []byte) [][]byte {
	// helm template separates docs with `\n---\n` lines.
	parts := bytes.Split(b, []byte("\n---\n"))
	out := make([][]byte, 0, len(parts))
	out = append(out, parts...)
	return out
}

func writeFile(path, content string) error {
	return execOK(exec.Command("sh", "-c", "cat > "+path), content)
}

func execOK(cmd *exec.Cmd, stdin string) error {
	cmd.Stdin = strings.NewReader(stdin)
	return cmd.Run()
}

// findByKind returns all rendered docs whose kind matches.
func findByKind(docs []map[string]any, kind string) []map[string]any {
	out := []map[string]any{}
	for _, d := range docs {
		if k, _ := d["kind"].(string); k == kind {
			out = append(out, d)
		}
	}
	return out
}

// metaName returns metadata.name from a rendered doc.
func metaName(d map[string]any) string {
	meta, _ := d["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	return name
}

// TestG16_DefaultRender_IncludesNetworkPolicies verifies the chart ships
// at least one NetworkPolicy by default.
func TestG16_DefaultRender_IncludesNetworkPolicies(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")
	require.NotEmpty(t, policies,
		"chart must ship at least one NetworkPolicy by default (Epic 17 G16)")
}

// TestG16_DefaultRender_HasDefaultDenyIngress verifies the workspace
// ingress policy denies-by-default with an explicit narrow allowance for
// the API proxy. NetworkPolicy semantics: any pod matching podSelector
// receives ONLY the listed ingress rules; everything else is denied. So
// the contract is:
//
//   - The policy exists, scoped to the workspace pod selector.
//   - Its policyTypes include "Ingress".
//   - Its ingress block lists exactly the API proxy on agentd port 4097
//     (and opencode 4096 for SSE/proxy paths).
//
// We deliberately do NOT assert "ingress list is empty" — a true empty
// list would break the proxy. What matters is that no other clients can
// reach the workspace pod.
func TestG16_DefaultRender_HasDefaultDenyIngress(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	var found bool
	for _, p := range policies {
		name := metaName(p)
		if !strings.Contains(name, "workspace-default-deny") {
			continue
		}
		spec, _ := p["spec"].(map[string]any)
		policyTypes, _ := spec["policyTypes"].([]any)
		hasIngress := false
		for _, pt := range policyTypes {
			if pt == "Ingress" {
				hasIngress = true
			}
		}
		require.True(t, hasIngress,
			"default-deny policy %q must declare policyTypes: [Ingress, ...]", name)

		ingress, _ := spec["ingress"].([]any)
		// Two ingress rules expected:
		//   1. API server pods → 4096/4097/4098 (proxy + agentd traffic).
		//   2. Controller pods → 4098 (Epic 22 health-endpoint polling).
		// Without rule 2 the controller's /v1/healthz probe times out,
		// trips the 3-strike threshold, and kills the workspace pod in
		// an infinite loop.
		require.Len(t, ingress, 2,
			"default-deny policy %q must have two allow rules (API and controller)", name)

		// Locate and verify the API proxy rule (allows 4097 from component=api).
		var apiRule, controllerRule map[string]any
		for _, r := range ingress {
			rm, _ := r.(map[string]any)
			from, _ := rm["from"].([]any)
			if len(from) == 0 {
				continue
			}
			fromMap, _ := from[0].(map[string]any)
			podSel, _ := fromMap["podSelector"].(map[string]any)
			matchLabels, _ := podSel["matchLabels"].(map[string]any)
			switch matchLabels["app.kubernetes.io/component"] {
			case "api":
				apiRule = rm
			case "controller":
				controllerRule = rm
			}
		}

		require.NotNil(t, apiRule,
			"default-deny policy %q must include an ingress rule for the API server", name)
		require.NotNil(t, controllerRule,
			"default-deny policy %q must include an ingress rule for the controller (Epic 22 health polling)", name)

		// API rule: must allow 4097.
		apiPorts, _ := apiRule["ports"].([]any)
		var foundAgentdPort bool
		for _, p := range apiPorts {
			pm := p.(map[string]any)
			if port := pm["port"]; port == float64(4097) || port == 4097 {
				foundAgentdPort = true
			}
		}
		require.True(t, foundAgentdPort,
			"API ingress rule must allow agentd port 4097")

		// Controller rule: must allow at least 4098 (health probes).
		controllerPorts, _ := controllerRule["ports"].([]any)
		var foundAdminPort bool
		for _, p := range controllerPorts {
			pm := p.(map[string]any)
			if port := pm["port"]; port == float64(4098) || port == 4098 {
				foundAdminPort = true
			}
		}
		require.True(t, foundAdminPort,
			"Controller ingress rule must allow admin port 4098 (Epic 22)")

		found = true
		break
	}
	require.True(t, found, "default-deny ingress NetworkPolicy not found in default render")
}

// TestG16_DefaultRender_HasWorkspaceEgressAllowList verifies that the
// chart ships an egress-allow policy that permits at least DNS so
// sandbox pods can resolve LLM endpoints. Without DNS, every workspace
// is broken on first boot.
func TestG16_DefaultRender_HasWorkspaceEgressAllowList(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	var found bool
	for _, p := range policies {
		name := metaName(p)
		if !strings.Contains(name, "workspace-egress") {
			continue
		}
		spec, _ := p["spec"].(map[string]any)
		policyTypes, _ := spec["policyTypes"].([]any)
		hasEgress := false
		for _, pt := range policyTypes {
			if pt == "Egress" {
				hasEgress = true
			}
		}
		require.True(t, hasEgress,
			"workspace-egress policy %q must declare policyTypes: [Egress, ...]", name)
		// Must permit at least one egress entry (DNS).
		egress, _ := spec["egress"].([]any)
		require.NotEmpty(t, egress,
			"workspace-egress policy %q must have at least one egress rule (DNS)", name)
		found = true
		break
	}
	require.True(t, found, "workspace-egress NetworkPolicy not found in default render")
}

// TestG16_NetworkPolicyDisabled_OmitsResources verifies operators can
// opt out by setting networkPolicy.enabled=false. This is for clusters
// that already enforce equivalent policies via Cilium CRDs or admission
// controllers.
func TestG16_NetworkPolicyDisabled_OmitsResources(t *testing.T) {
	docs := helmTemplate(t, "networkPolicy:\n  enabled: false\n")
	policies := findByKind(docs, "NetworkPolicy")
	require.Empty(t, policies,
		"setting networkPolicy.enabled=false must omit all chart NetworkPolicies")
}

// TestG16_PoliciesScopeToWorkspaceNamespace verifies the policies are
// rendered into the workspace namespace, not the platform's release
// namespace. The release namespace runs API/controller, which need their
// own policies; mixing them with workspace policies leads to lockout
// during upgrades.
func TestG16_PoliciesScopeToWorkspaceNamespace(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	for _, p := range policies {
		meta, _ := p["metadata"].(map[string]any)
		ns, _ := meta["namespace"].(string)
		// Workspace policies should target the workspace namespace, which
		// defaults to the release namespace when namespace.create=false.
		// In our test setup with -n test-ns, the workspace namespace
		// resolves to test-ns. We assert it's set (not empty).
		require.NotEmpty(t, ns,
			"NetworkPolicy %q must have an explicit namespace", metaName(p))
	}
}

// =============================================================================
// G26 — Datastore credentials + datastore NetworkPolicies
// =============================================================================
//
// These tests guard the Critical finding from worklog 0089 (RT-4.5):
//
//   - postgres-password defaulted to the literal string "changeme"
//   - redis-password defaulted to "" (Valkey reported `requirepass` empty)
//   - No NetworkPolicy gated postgres or valkey ingress
//
// The chart fix has three contracts:
//
//   1. If the operator does not supply a postgres password, the chart
//      auto-generates a random 32+ character one (mirrors jwtSecret).
//      No literal "changeme" may appear in the rendered Secret.
//   2. Same for redis-password.
//   3. When `datastore.networkPolicy.enabled` (default true) the chart
//      renders two NetworkPolicy objects naming `app=postgres` and
//      `app=valkey` selectors, each with an ingress rule restricting
//      traffic to the API + migrate-job pod selectors only.
//
// Each test deliberately reverses to FAIL if the contract drifts (mutation-
// validated: revert the fix in values.yaml or the new template; the test
// must turn red).

func secretValue(t *testing.T, sec map[string]any, key string) string {
	t.Helper()
	if sd, ok := sec["stringData"].(map[string]any); ok {
		if v, ok := sd[key].(string); ok {
			return v
		}
	}
	if d, ok := sec["data"].(map[string]any); ok {
		if v, ok := d[key].(string); ok {
			return v
		}
	}
	return ""
}

// TestG26_DefaultRender_PostgresPasswordIsGenerated proves that a fresh
// `helm template` with no overrides does NOT render the literal
// "changeme" as the postgres password. Pre-fix this test FAILs because
// values.yaml seeded the default.
func TestG26_DefaultRender_PostgresPasswordIsGenerated(t *testing.T) {
	docs := helmTemplate(t, "")
	// The chart's secret is named per release; helmTemplate uses
	// release "test" (see helmTemplate impl above).
	var sec map[string]any
	for _, d := range docs {
		if d["kind"] == "Secret" {
			meta, _ := d["metadata"].(map[string]any)
			ns, _ := meta["namespace"].(string)
			// Only consider the platform credentials Secret, not any
			// per-workspace ephemeral secrets.
			if ns == "test-ns" {
				sec = d
				break
			}
		}
	}
	require.NotNil(t, sec, "platform credentials Secret must be rendered by default")

	pw := secretValue(t, sec, "postgres-password")
	require.NotEqual(t, "changeme", pw,
		"postgres-password must NOT default to the literal 'changeme' (G26)")
	require.GreaterOrEqual(t, len(pw), 24,
		"auto-generated postgres-password must be at least 24 chars; got %d", len(pw))
}

// TestG26_DefaultRender_RedisPasswordIsGenerated mirrors the postgres
// test for the Valkey/Redis password. Pre-fix the value defaulted to
// the empty string, which Valkey treats as "no auth required".
func TestG26_DefaultRender_RedisPasswordIsGenerated(t *testing.T) {
	docs := helmTemplate(t, "")
	var sec map[string]any
	for _, d := range docs {
		if d["kind"] == "Secret" {
			meta, _ := d["metadata"].(map[string]any)
			if ns, _ := meta["namespace"].(string); ns == "test-ns" {
				sec = d
				break
			}
		}
	}
	require.NotNil(t, sec, "platform credentials Secret must be rendered")

	pw := secretValue(t, sec, "redis-password")
	require.NotEmpty(t, pw,
		"redis-password must NOT default to empty (Valkey requirepass would be unset; G26)")
	require.GreaterOrEqual(t, len(pw), 24,
		"auto-generated redis-password must be at least 24 chars; got %d", len(pw))
}

// TestG26_OperatorOverride_PostgresPasswordIsRespected proves the
// operator can still pin a specific password (no surprise rotation on
// upgrade). This guards the rotation-safety property: an existing
// installation with a known password must keep it across `helm upgrade`.
func TestG26_OperatorOverride_PostgresPasswordIsRespected(t *testing.T) {
	docs := helmTemplate(t, "externalSecret:\n  postgresPassword: \"operator-supplied-9876\"\n  redisPassword: \"operator-redis-1234\"\n")
	var sec map[string]any
	for _, d := range docs {
		if d["kind"] == "Secret" {
			meta, _ := d["metadata"].(map[string]any)
			if ns, _ := meta["namespace"].(string); ns == "test-ns" {
				sec = d
				break
			}
		}
	}
	require.NotNil(t, sec)
	require.Equal(t, "operator-supplied-9876", secretValue(t, sec, "postgres-password"))
	require.Equal(t, "operator-redis-1234", secretValue(t, sec, "redis-password"))
}

// TestG26_DefaultRender_HasPostgresIngressPolicy verifies a NetworkPolicy
// named per the chart's helper exists, selects pods with `app=postgres`,
// and has at least one ingress rule restricting the source.
func TestG26_DefaultRender_HasPostgresIngressPolicy(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	var pgPolicy map[string]any
	for _, p := range policies {
		spec, _ := p["spec"].(map[string]any)
		sel, _ := spec["podSelector"].(map[string]any)
		ml, _ := sel["matchLabels"].(map[string]any)
		if app, _ := ml["app"].(string); app == "postgres" {
			pgPolicy = p
			break
		}
	}
	require.NotNil(t, pgPolicy,
		"a NetworkPolicy selecting `app=postgres` must be rendered by default (G26)")

	spec, _ := pgPolicy["spec"].(map[string]any)
	policyTypes, _ := spec["policyTypes"].([]any)
	require.Contains(t, policyTypes, "Ingress",
		"postgres NetworkPolicy must declare Ingress in policyTypes")
	ingress, _ := spec["ingress"].([]any)
	require.NotEmpty(t, ingress,
		"postgres NetworkPolicy must have at least one ingress rule")
}

// TestG26_DefaultRender_HasValkeyIngressPolicy is the Valkey twin of the
// above. Same shape, different selector.
func TestG26_DefaultRender_HasValkeyIngressPolicy(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	var vkPolicy map[string]any
	for _, p := range policies {
		spec, _ := p["spec"].(map[string]any)
		sel, _ := spec["podSelector"].(map[string]any)
		ml, _ := sel["matchLabels"].(map[string]any)
		if app, _ := ml["app"].(string); app == "valkey" {
			vkPolicy = p
			break
		}
	}
	require.NotNil(t, vkPolicy,
		"a NetworkPolicy selecting `app=valkey` must be rendered by default (G26)")

	spec, _ := vkPolicy["spec"].(map[string]any)
	policyTypes, _ := spec["policyTypes"].([]any)
	require.Contains(t, policyTypes, "Ingress")
	ingress, _ := spec["ingress"].([]any)
	require.NotEmpty(t, ingress,
		"valkey NetworkPolicy must have at least one ingress rule")
}

// TestG26_DatastoreNetworkPolicy_OptOut lets operators who manage their
// own policies disable the chart's datastore policies without having
// to disable the workspace policies (which are critical and should
// stay on by default). Different toggles, separate concerns.
func TestG26_DatastoreNetworkPolicy_OptOut(t *testing.T) {
	docs := helmTemplate(t, "datastore:\n  networkPolicy:\n    enabled: false\n")
	policies := findByKind(docs, "NetworkPolicy")
	for _, p := range policies {
		spec, _ := p["spec"].(map[string]any)
		sel, _ := spec["podSelector"].(map[string]any)
		ml, _ := sel["matchLabels"].(map[string]any)
		app, _ := ml["app"].(string)
		require.NotEqual(t, "postgres", app,
			"datastore.networkPolicy.enabled=false must omit postgres NetworkPolicy")
		require.NotEqual(t, "valkey", app,
			"datastore.networkPolicy.enabled=false must omit valkey NetworkPolicy")
	}
}

// =============================================================================
// G2 — Workspace ValidatingWebhookConfiguration + controller flag wiring
// =============================================================================
//
// Closes F1.2.1, F1.2.2, F1.2.9, RT-2.18, RT-6.10, RT-6.1. The chart-side
// fix is two contracts:
//
//   1. ValidatingWebhookConfiguration includes a webhook for `workspaces`
//      pointing at /validate-llmsafespaces-dev-v1-workspace.
//   2. The controller deployment passes --allowed-image-registries,
//      --allowed-storage-class-names, and --max-workspace-storage-gi
//      to the controller binary, populated from values.yaml.

// findControllerArgs locates the controller container's args list in
// the rendered Deployment.
func findControllerArgs(t *testing.T, docs []map[string]any) []string {
	t.Helper()
	for _, d := range docs {
		if d["kind"] != "Deployment" {
			continue
		}
		meta, _ := d["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if !strings.Contains(name, "controller") {
			continue
		}
		spec, _ := d["spec"].(map[string]any)
		tmpl, _ := spec["template"].(map[string]any)
		podSpec, _ := tmpl["spec"].(map[string]any)
		containers, _ := podSpec["containers"].([]any)
		if len(containers) == 0 {
			continue
		}
		c, _ := containers[0].(map[string]any)
		raw, _ := c["args"].([]any)
		out := make([]string, 0, len(raw))
		for _, a := range raw {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// TestG2_WebhookConfig_IncludesWorkspace asserts the
// ValidatingWebhookConfiguration carries a webhook for the workspaces
// resource. Without this entry the workspace webhook never receives
// admission requests and the registry allow-list is bypassed for any
// kubectl-direct workspace creation.
func TestG2_WebhookConfig_IncludesWorkspace(t *testing.T) {
	docs := helmTemplate(t, "")
	for _, d := range docs {
		if d["kind"] != "ValidatingWebhookConfiguration" {
			continue
		}
		webhooks, _ := d["webhooks"].([]any)
		var sawWorkspace bool
		for _, w := range webhooks {
			wm, _ := w.(map[string]any)
			cc, _ := wm["clientConfig"].(map[string]any)
			svc, _ := cc["service"].(map[string]any)
			path, _ := svc["path"].(string)
			if path == "/validate-llmsafespaces-dev-v1-workspace" {
				sawWorkspace = true
				rules, _ := wm["rules"].([]any)
				require.NotEmpty(t, rules, "workspace webhook must declare at least one rule")
				rule, _ := rules[0].(map[string]any)
				resources, _ := rule["resources"].([]any)
				require.Contains(t, resources, "workspaces")
				ops, _ := rule["operations"].([]any)
				require.Contains(t, ops, "CREATE")
				require.Contains(t, ops, "UPDATE")
				break
			}
		}
		require.True(t, sawWorkspace,
			"ValidatingWebhookConfiguration must include a webhook for /validate-llmsafespaces-dev-v1-workspace")
		return
	}
	t.Fatal("no ValidatingWebhookConfiguration rendered")
}

// TestG2_ControllerArgs_PassesAllowedImageRegistries asserts the
// controller deployment receives the --allowed-image-registries flag
// populated from values.yaml. Default values.yaml ships a non-empty
// list (ghcr.io/lenaxia/) so the flag must appear by default.
func TestG2_ControllerArgs_PassesAllowedImageRegistries(t *testing.T) {
	docs := helmTemplate(t, "")
	args := findControllerArgs(t, docs)
	require.NotEmpty(t, args, "controller container must have args")

	var found string
	for _, a := range args {
		if strings.HasPrefix(a, "--allowed-image-registries=") {
			found = a
			break
		}
	}
	require.NotEmpty(t, found,
		"--allowed-image-registries flag must be set when webhooks.allowedImageRegistries is non-empty")
	require.Contains(t, found, "ghcr.io/lenaxia/",
		"default --allowed-image-registries must include ghcr.io/lenaxia/ (G2)")
}

// TestG2_ControllerArgs_OmitsAllowedRegistriesWhenEmpty validates the
// negative-case rendering: with an empty list the flag is omitted so
// the controller's default (also empty list) takes effect.
func TestG2_ControllerArgs_OmitsAllowedRegistriesWhenEmpty(t *testing.T) {
	docs := helmTemplate(t, "webhooks:\n  allowedImageRegistries: []\n")
	args := findControllerArgs(t, docs)
	for _, a := range args {
		require.False(t, strings.HasPrefix(a, "--allowed-image-registries="),
			"--allowed-image-registries must NOT be set when the values list is empty (avoids '--flag=' which Go flag parses as empty)")
	}
}

// TestG2_ControllerArgs_PassesMaxStorageGi asserts the upper-bound
// flag flows through. Default 1024 must be the rendered value.
func TestG2_ControllerArgs_PassesMaxStorageGi(t *testing.T) {
	docs := helmTemplate(t, "")
	args := findControllerArgs(t, docs)
	var found string
	for _, a := range args {
		if strings.HasPrefix(a, "--max-workspace-storage-gi=") {
			found = a
			break
		}
	}
	require.Equal(t, "--max-workspace-storage-gi=1024", found,
		"controller must receive the default 1024 GiB upper-bound flag (G2 / RT-6.1)")
}

// TestG2_ControllerArgs_HonorsOperatorOverride confirms the operator
// can change the upper bound and add storage class allow-list entries
// through values.yaml, and the deployment re-renders with the new
// values.
func TestG2_ControllerArgs_HonorsOperatorOverride(t *testing.T) {
	docs := helmTemplate(t, `webhooks:
  allowedImageRegistries:
    - "registry.k8s.io/"
  allowedStorageClassNames:
    - "longhorn"
    - "gp3"
  maxWorkspaceStorageGi: 64
`)
	args := findControllerArgs(t, docs)
	require.NotEmpty(t, args)

	asMap := map[string]string{}
	for _, a := range args {
		if i := strings.Index(a, "="); i > 0 {
			asMap[a[:i]] = a[i+1:]
		}
	}
	require.Equal(t, "registry.k8s.io/", asMap["--allowed-image-registries"])
	require.Equal(t, "longhorn,gp3", asMap["--allowed-storage-class-names"])
	require.Equal(t, "64", asMap["--max-workspace-storage-gi"])
}

// =============================================================================
// F1 / F5 — Org-suspension wiring (worklog 0372)
// =============================================================================
//
// D20 org-level workspace suspension is driven by the controller polling an
// internal API endpoint. Pre-fix the chart did NOT wire --api-service-url nor
// the shared LLMSAFESPACES_INTERNAL_TOKEN, so the feature was inert in every
// Helm deployment and the internal endpoint was unauthenticated. These tests
// lock in:
//   1. The controller deployment receives --api-service-url (F1).
//   2. Both API and controller deployments mount LLMSAFESPACES_INTERNAL_TOKEN
//      from the same Secret key (F1+F5).
//   3. The credentials Secret carries an auto-generated internal-token (F1).
//   4. The opt-in API ingress NetworkPolicy is absent by default and present
//      when networkPolicy.apiIngressRestricted=true (F5).

// containerEnvNames returns the set of env var names declared on the named
// container in a Deployment doc.
func containerEnvNames(deploy map[string]any, name string) map[string]bool {
	out := map[string]bool{}
	c := containerByName(deploy, name)
	if c == nil {
		return out
	}
	env, _ := c["env"].([]any)
	for _, e := range env {
		em, _ := e.(map[string]any)
		if n, ok := em["name"].(string); ok {
			out[n] = true
		}
	}
	return out
}

// TestF1_ControllerArgs_PassesApiServiceURL asserts the controller deployment
// receives --api-service-url so org-suspension is functional (D20). Pre-fix the
// flag was absent and OrgStatusClient was always nil in Helm deployments.
func TestF1_ControllerArgs_PassesApiServiceURL(t *testing.T) {
	docs := helmTemplate(t, "")
	args := findControllerArgs(t, docs)
	require.NotEmpty(t, args)

	var found string
	for _, a := range args {
		if strings.HasPrefix(a, "--api-service-url=") {
			found = a
			break
		}
	}
	require.NotEmpty(t, found,
		"controller deployment must pass --api-service-url so org-suspension is functional (F1)")
	// Default-derivation: the chart derives the in-cluster API service URL from
	// the release name + namespace + API port.
	require.Contains(t, found, "-api.",
		"--api-service-url must derive the in-cluster API service URL by default, got %q", found)
	require.Contains(t, found, ":8080",
		"--api-service-url must target the API service port, got %q", found)
}

// TestF1_ControllerArgs_ApiServiceURL_HonorsOverride confirms an operator can
// point the controller at a custom API URL.
func TestF1_ControllerArgs_ApiServiceURL_HonorsOverride(t *testing.T) {
	docs := helmTemplate(t, "controller:\n  apiServiceURL: \"http://api.custom.svc:9090\"\n")
	args := findControllerArgs(t, docs)
	var found string
	for _, a := range args {
		if strings.HasPrefix(a, "--api-service-url=") {
			found = a
			break
		}
	}
	require.Equal(t, "--api-service-url=http://api.custom.svc:9090", found,
		"controller.apiServiceURL override must flow through verbatim (F1)")
}

// TestF1_InternalTokenEnv_OnBothDeployments asserts both the API and the
// controller mount LLMSAFESPACES_INTERNAL_TOKEN from the credentials Secret, so
// the fail-closed internal endpoint is reachable by the controller (F1+F5).
func TestF1_InternalTokenEnv_OnBothDeployments(t *testing.T) {
	docs := helmTemplate(t, "")

	apiDeploy := findDeploymentByNameSubstr(docs, "-api")
	require.NotNil(t, apiDeploy, "API Deployment must be rendered")
	require.Contains(t, containerEnvNames(apiDeploy, "api"), "LLMSAFESPACES_INTERNAL_TOKEN",
		"API deployment must mount LLMSAFESPACES_INTERNAL_TOKEN (the internal endpoint fails closed without it; F5)")

	controllerDeploy := findDeploymentByNameSubstr(docs, "-controller")
	require.NotNil(t, controllerDeploy, "controller Deployment must be rendered")
	require.Contains(t, containerEnvNames(controllerDeploy, "manager"), "LLMSAFESPACES_INTERNAL_TOKEN",
		"controller deployment must mount LLMSAFESPACES_INTERNAL_TOKEN so it can authenticate the internal org-status poll (F1)")
}

// TestF1_SecretIncludesInternalToken asserts the credentials Secret carries an
// auto-generated internal-token key (so the env mounts resolve on a fresh
// install with no operator overrides).
func TestF1_SecretIncludesInternalToken(t *testing.T) {
	docs := helmTemplate(t, "")
	var sec map[string]any
	for _, d := range docs {
		if d["kind"] == "Secret" {
			if meta, _ := d["metadata"].(map[string]any); meta != nil {
				if ns, _ := meta["namespace"].(string); ns == "test-ns" {
					sec = d
					break
				}
			}
		}
	}
	require.NotNil(t, sec, "platform credentials Secret must be rendered")
	tok := secretValue(t, sec, "internal-token")
	require.NotEmpty(t, tok, "Secret must include an auto-generated internal-token key (F1)")
	require.GreaterOrEqual(t, len(tok), 24,
		"auto-generated internal-token must be at least 24 chars; got %d", len(tok))
}

// TestF5_ApiNetworkPolicy_DefaultOff asserts the API ingress NetworkPolicy is
// NOT rendered by default (it is opt-in: an incomplete allowlist would lock
// users out, and the internal endpoint is already token-gated).
func TestF5_ApiNetworkPolicy_DefaultOff(t *testing.T) {
	docs := helmTemplate(t, "")
	for _, p := range findByKind(docs, "NetworkPolicy") {
		require.NotContains(t, metaName(p), "api-ingress",
			"API ingress NetworkPolicy must be absent by default (opt-in via networkPolicy.apiIngressRestricted; F5)")
	}
}

// TestF5_ApiNetworkPolicy_OptIn asserts the policy renders with controller +
// user-traffic + kube-system allow rules when apiIngressRestricted=true.
func TestF5_ApiNetworkPolicy_OptIn(t *testing.T) {
	docs := helmTemplate(t, "networkPolicy:\n  apiIngressRestricted: true\n")
	var apiPolicy map[string]any
	for _, p := range findByKind(docs, "NetworkPolicy") {
		if strings.Contains(metaName(p), "api-ingress") {
			apiPolicy = p
			break
		}
	}
	require.NotNil(t, apiPolicy, "API ingress NetworkPolicy must render when apiIngressRestricted=true (F5)")

	spec, _ := apiPolicy["spec"].(map[string]any)
	policyTypes, _ := spec["policyTypes"].([]any)
	require.Contains(t, policyTypes, "Ingress", "API NetworkPolicy must declare Ingress in policyTypes")
	ingress, _ := spec["ingress"].([]any)
	require.GreaterOrEqual(t, len(ingress), 3,
		"API NetworkPolicy must admit controller + user-traffic + kube-system (3 ingress rules)")
}

// =============================================================================
// G5 / F1.3.x — RBAC tightening (worklog 0107)
// =============================================================================

// findResources returns all rendered docs of the given Kind.
func findResources(docs []map[string]any, kind string) []map[string]any {
	out := []map[string]any{}
	for _, d := range docs {
		if d["kind"] == kind {
			out = append(out, d)
		}
	}
	return out
}

// resourceVerbs walks the rules of a Role/ClusterRole doc and returns
// a {apiGroup/resource: verbs[]} map for assertion.
func resourceVerbs(doc map[string]any) map[string][]string {
	out := map[string][]string{}
	rules, _ := doc["rules"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		groups, _ := rule["apiGroups"].([]any)
		resources, _ := rule["resources"].([]any)
		verbs, _ := rule["verbs"].([]any)
		var verbStrs []string
		for _, v := range verbs {
			if s, ok := v.(string); ok {
				verbStrs = append(verbStrs, s)
			}
		}
		for _, g := range groups {
			for _, res := range resources {
				key := fmt.Sprintf("%s/%s", g, res)
				out[key] = append(out[key], verbStrs...)
			}
		}
	}
	return out
}

// TestG5_DefaultIsNamespaceScope asserts the post-fix default `rbac.scope`
// is "namespace" — operators no longer get cluster-wide secrets/pods
// access by default.
func TestG5_DefaultIsNamespaceScope(t *testing.T) {
	docs := helmTemplate(t, "")
	clusterRoles := findResources(docs, "ClusterRole")
	// Allow ONLY the storageclass-reader ClusterRole — the cluster
	// scope ClusterRole must NOT be rendered by default.
	for _, cr := range clusterRoles {
		name := metaName(cr)
		require.NotContains(t, name, "controller-cluster",
			"default install must NOT render the cluster-scope ClusterRole; got %q", name)
	}
}

// TestG5_ClusterScopeOptInRendersClusterRole asserts the cluster
// scope is preserved as an opt-in. Read-only watch on pods/secrets
// IS permitted (controller-runtime informer cache requires it);
// CRUD verbs are still forbidden cluster-wide.
func TestG5_ClusterScopeOptInRendersClusterRole(t *testing.T) {
	docs := helmTemplate(t, "rbac:\n  scope: cluster\n")
	clusterRoles := findResources(docs, "ClusterRole")
	var sawClusterScope bool
	mutating := map[string]struct{}{
		"create": {}, "update": {}, "patch": {}, "delete": {}, "deletecollection": {},
	}
	for _, cr := range clusterRoles {
		if !strings.Contains(metaName(cr), "controller-cluster") {
			continue
		}
		sawClusterScope = true
		rules, _ := cr["rules"].([]any)
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			groups, _ := rule["apiGroups"].([]any)
			resources, _ := rule["resources"].([]any)
			verbs, _ := rule["verbs"].([]any)
			isCore := false
			for _, g := range groups {
				if s, _ := g.(string); s == "" {
					isCore = true
				}
			}
			if !isCore {
				continue
			}
			for _, res := range resources {
				resStr, _ := res.(string)
				if resStr != "secrets" && resStr != "pods" {
					continue
				}
				for _, v := range verbs {
					vStr, _ := v.(string)
					_, mut := mutating[vStr]
					require.False(t, mut,
						"cluster ClusterRole must NOT grant cluster-wide mutating verb %q on %s (G5 / F1.3.3)",
						vStr, resStr)
				}
			}
		}
	}
	require.True(t, sawClusterScope,
		"rbac.scope=cluster must render the controller-cluster ClusterRole")
}

// TestF132_LeasesAreNamespaceScoped asserts coordination.k8s.io/leases
// is granted via Role (namespace), not ClusterRole.
func TestF132_LeasesAreNamespaceScoped(t *testing.T) {
	docs := helmTemplate(t, "")
	clusterRoles := findResources(docs, "ClusterRole")
	for _, cr := range clusterRoles {
		rv := resourceVerbs(cr)
		require.NotContains(t, rv, "coordination.k8s.io/leases",
			"leases must not be cluster-scoped (F1.3.2); found in ClusterRole %q", metaName(cr))
	}
	// And the Role for leader election must contain leases.
	roles := findResources(docs, "Role")
	var sawLeases bool
	for _, role := range roles {
		rv := resourceVerbs(role)
		if _, ok := rv["coordination.k8s.io/leases"]; ok {
			sawLeases = true
		}
	}
	require.True(t, sawLeases, "leases must be granted via a namespace-scoped Role")
}

// TestF134_APIDoesNotGrantRuntimeEnvironments asserts the API SA Role
// does not include runtimeenvironments (unused per audit).
func TestF134_APIDoesNotGrantRuntimeEnvironments(t *testing.T) {
	docs := helmTemplate(t, "")
	roles := findResources(docs, "Role")
	for _, role := range roles {
		name := metaName(role)
		if !strings.Contains(name, "-api") {
			continue
		}
		rv := resourceVerbs(role)
		require.NotContains(t, rv, "llmsafespaces.dev/runtimeenvironments",
			"API Role %q must NOT grant runtimeenvironments (F1.3.4)", name)
	}
}

// TestF135_APIDoesNotGrantPodsLog asserts the API SA Role does not
// include pods/log (unused per audit).
func TestF135_APIDoesNotGrantPodsLog(t *testing.T) {
	docs := helmTemplate(t, "")
	roles := findResources(docs, "Role")
	for _, role := range roles {
		name := metaName(role)
		if !strings.Contains(name, "-api") {
			continue
		}
		rv := resourceVerbs(role)
		require.NotContains(t, rv, "/pods/log",
			"API Role %q must NOT grant pods/log (F1.3.5)", name)
	}
}

// TestF131_ControllerDoesNotGrantUnusedResources asserts services and
// configmaps are removed from the controller's grants (F1.3.1).
func TestF131_ControllerDoesNotGrantUnusedResources(t *testing.T) {
	docs := helmTemplate(t, "rbac:\n  scope: cluster\n")
	for _, kind := range []string{"Role", "ClusterRole"} {
		for _, doc := range findResources(docs, kind) {
			name := metaName(doc)
			if !strings.Contains(name, "controller") {
				continue
			}
			rv := resourceVerbs(doc)
			require.NotContains(t, rv, "/services",
				"%s %q must NOT grant services (F1.3.1)", kind, name)
			require.NotContains(t, rv, "/configmaps",
				"%s %q must NOT grant configmaps (F1.3.1)", kind, name)
		}
	}
}

// TestF137_StorageClassesIsAlwaysClusterRole asserts storageclasses
// is granted via a ClusterRole regardless of rbac.scope, so it doesn't
// silently disappear in namespace mode.
func TestF137_StorageClassesIsAlwaysClusterRole(t *testing.T) {
	for _, scope := range []string{"namespace", "cluster"} {
		t.Run("scope="+scope, func(t *testing.T) {
			docs := helmTemplate(t, fmt.Sprintf("rbac:\n  scope: %s\n", scope))
			clusterRoles := findResources(docs, "ClusterRole")
			var sawSC bool
			for _, cr := range clusterRoles {
				rv := resourceVerbs(cr)
				if _, ok := rv["storage.k8s.io/storageclasses"]; ok {
					sawSC = true
				}
			}
			require.True(t, sawSC,
				"storageclasses must be granted via a ClusterRole when scope=%s (F1.3.7)", scope)
		})
	}
}

// =============================================================================
// Helm audit fixes (worklog 0174) — regression tests for 7 bugs found in
// the chart audit. Each test is designed to turn red if the corresponding
// fix is accidentally reverted.
// =============================================================================

// findDeploymentByNameSubstr returns the first Deployment whose metadata.name
// contains the given substring.
func findDeploymentByNameSubstr(docs []map[string]any, substr string) map[string]any {
	for _, d := range docs {
		if d["kind"] != "Deployment" {
			continue
		}
		if strings.Contains(metaName(d), substr) {
			return d
		}
	}
	return nil
}

// findServiceByNameSubstr returns the first Service whose metadata.name
// contains the given substring.
func findServiceByNameSubstr(docs []map[string]any, substr string) map[string]any {
	for _, d := range docs {
		if d["kind"] != "Service" {
			continue
		}
		if strings.Contains(metaName(d), substr) {
			return d
		}
	}
	return nil
}

// containerByName returns the first container spec matching the given name
// from a Deployment doc.
func containerByName(deploy map[string]any, name string) map[string]any {
	spec, _ := deploy["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		if n, _ := cm["name"].(string); n == name {
			return cm
		}
	}
	return nil
}

// podSecCtx returns the pod-level securityContext from a Deployment doc.
func podSecCtx(deploy map[string]any) map[string]any {
	spec, _ := deploy["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	ctx, _ := podSpec["securityContext"].(map[string]any)
	return ctx
}

// TestF1_MCPResourcesUseReleaseNamespace guards the F1 fix: both the MCP
// Deployment and Service must render into .Release.Namespace, not into
// whatever .Values.namespace.name resolves to (undefined = "").
func TestF1_MCPResourcesUseReleaseNamespace(t *testing.T) {
	docs := helmTemplate(t, "mcp:\n  enabled: true\n")

	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.NotNil(t, deploy, "MCP Deployment must be rendered when mcp.enabled=true")
	meta, _ := deploy["metadata"].(map[string]any)
	ns, _ := meta["namespace"].(string)
	require.Equal(t, "test-ns", ns,
		"MCP Deployment namespace must equal .Release.Namespace (F1 fix: was .Values.namespace.name)")

	svc := findServiceByNameSubstr(docs, "-mcp")
	require.NotNil(t, svc, "MCP Service must be rendered when mcp.enabled=true")
	smeta, _ := svc["metadata"].(map[string]any)
	sns, _ := smeta["namespace"].(string)
	require.Equal(t, "test-ns", sns,
		"MCP Service namespace must equal .Release.Namespace (F1 fix)")
}

// TestF2_MCPProbesAreTCPSocket guards the F2 fix: the MCP container's
// liveness and readiness probes must use tcpSocket, not httpGet. The old
// httpGet /sse hung indefinitely because /sse is a streaming endpoint.
func TestF2_MCPProbesAreTCPSocket(t *testing.T) {
	docs := helmTemplate(t, "mcp:\n  enabled: true\n")

	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.NotNil(t, deploy, "MCP Deployment must be rendered")
	c := containerByName(deploy, "mcp")
	require.NotNil(t, c, "mcp container must exist")

	liveness, _ := c["livenessProbe"].(map[string]any)
	require.NotNil(t, liveness, "MCP container must have a livenessProbe")
	_, hasTCP := liveness["tcpSocket"]
	_, hasHTTP := liveness["httpGet"]
	require.True(t, hasTCP, "MCP livenessProbe must use tcpSocket (F2 fix: httpGet /sse hung)")
	require.False(t, hasHTTP, "MCP livenessProbe must NOT use httpGet")

	readiness, _ := c["readinessProbe"].(map[string]any)
	require.NotNil(t, readiness, "MCP container must have a readinessProbe (F2 fix: was missing)")
	_, hasTCPR := readiness["tcpSocket"]
	require.True(t, hasTCPR, "MCP readinessProbe must use tcpSocket")
}

// TestF3_MCPSecurityContext guards the F3 fix: the MCP pod must have a
// podSecurityContext and containerSecurityContext that satisfy PSA restricted
// (the chart's own namespace default). Pre-fix, the pod had neither and was
// rejected immediately by admission.
func TestF3_MCPSecurityContext(t *testing.T) {
	docs := helmTemplate(t, "mcp:\n  enabled: true\n")

	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.NotNil(t, deploy)

	// Pod-level security context.
	psc := podSecCtx(deploy)
	require.NotNil(t, psc, "MCP Deployment must have a podSecurityContext (F3 fix)")
	require.Equal(t, true, psc["runAsNonRoot"],
		"MCP podSecurityContext.runAsNonRoot must be true (PSA restricted)")
	seccomp, _ := psc["seccompProfile"].(map[string]any)
	require.Equal(t, "RuntimeDefault", seccomp["type"],
		"MCP podSecurityContext.seccompProfile.type must be RuntimeDefault")

	// Container-level security context.
	c := containerByName(deploy, "mcp")
	require.NotNil(t, c)
	csc, _ := c["securityContext"].(map[string]any)
	require.NotNil(t, csc, "MCP container must have a securityContext (F3 fix)")
	require.Equal(t, false, csc["allowPrivilegeEscalation"],
		"MCP container.allowPrivilegeEscalation must be false")
	require.Equal(t, true, csc["readOnlyRootFilesystem"],
		"MCP container.readOnlyRootFilesystem must be true (F3 fix)")
	caps, _ := csc["capabilities"].(map[string]any)
	drop, _ := caps["drop"].([]any)
	var droppedAll bool
	for _, d := range drop {
		if d == "ALL" {
			droppedAll = true
		}
	}
	require.True(t, droppedAll, "MCP container must drop ALL capabilities (F3 fix)")
}

// TestF4_FrontendReadOnlyRootFilesystem guards the F4 fix: the frontend
// container must have readOnlyRootFilesystem=true with emptyDir volumes
// for the paths nginx needs to write. Pre-fix, readOnlyRootFilesystem was
// explicitly false.
func TestF4_FrontendReadOnlyRootFilesystem(t *testing.T) {
	docs := helmTemplate(t, "frontend:\n  enabled: true\n")

	deploy := findDeploymentByNameSubstr(docs, "-frontend")
	require.NotNil(t, deploy, "frontend Deployment must be rendered when frontend.enabled=true")

	c := containerByName(deploy, "frontend")
	require.NotNil(t, c, "frontend container must exist")
	csc, _ := c["securityContext"].(map[string]any)
	require.NotNil(t, csc, "frontend container must have a securityContext")
	require.Equal(t, true, csc["readOnlyRootFilesystem"],
		"frontend container.readOnlyRootFilesystem must be true (F4 fix: was false)")

	// Must have emptyDir volumes for the writable nginx paths.
	spec, _ := deploy["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	volumes, _ := podSpec["volumes"].([]any)
	volumeNames := map[string]bool{}
	for _, v := range volumes {
		vm, _ := v.(map[string]any)
		if name, ok := vm["name"].(string); ok {
			_, isEmptyDir := vm["emptyDir"]
			if isEmptyDir {
				volumeNames[name] = true
			}
		}
	}
	for _, required := range []string{"nginx-cache", "nginx-run", "tmp"} {
		require.True(t, volumeNames[required],
			"frontend Deployment must have an emptyDir volume %q for nginx writability (F4 fix)", required)
	}
}

// TestF5_AdditionalHostsHaveAPIPath guards the F5 fix: when additionalHosts
// is configured, every additional host's ingress rule must include both an
// /api path (to the API service) and a / path (to the frontend). Pre-fix,
// only the / path was generated, causing 502 for all API calls on extra hosts.
func TestF5_AdditionalHostsHaveAPIPath(t *testing.T) {
	docs := helmTemplate(t, `frontend:
  enabled: true
  ingress:
    enabled: true
    host: "primary.example.com"
    additionalHosts:
      - host: "extra.example.com"
`)

	var frontendIngress map[string]any
	for _, d := range docs {
		if d["kind"] != "Ingress" {
			continue
		}
		if strings.Contains(metaName(d), "frontend") {
			frontendIngress = d
			break
		}
	}
	require.NotNil(t, frontendIngress, "frontend Ingress must be rendered")

	spec, _ := frontendIngress["spec"].(map[string]any)
	rules, _ := spec["rules"].([]any)

	// Find the rule for extra.example.com.
	var extraRule map[string]any
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if h, _ := rm["host"].(string); h == "extra.example.com" {
			extraRule = rm
			break
		}
	}
	require.NotNil(t, extraRule,
		"Ingress must contain a rule for the additionalHost extra.example.com")

	http, _ := extraRule["http"].(map[string]any)
	paths, _ := http["paths"].([]any)

	var hasAPI, hasRoot bool
	for _, p := range paths {
		pm, _ := p.(map[string]any)
		path, _ := pm["path"].(string)
		if path == "/api" {
			hasAPI = true
		}
		if path == "/" {
			hasRoot = true
		}
	}
	require.True(t, hasAPI,
		"additionalHost rule must include /api path to the API service (F5 fix: was missing)")
	require.True(t, hasRoot,
		"additionalHost rule must include / path to the frontend service")
}

// TestF8_ValkeyPolicyAllowsMigrateJob guards the F8 fix: the Valkey
// NetworkPolicy must include an ingress rule for the migrate Job pod selector,
// symmetric with the Postgres policy. Pre-fix, only the API pod was allowed.
func TestF8_ValkeyPolicyAllowsMigrateJob(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	var vkPolicy map[string]any
	for _, p := range policies {
		spec, _ := p["spec"].(map[string]any)
		sel, _ := spec["podSelector"].(map[string]any)
		ml, _ := sel["matchLabels"].(map[string]any)
		if app, _ := ml["app"].(string); app == "valkey" {
			vkPolicy = p
			break
		}
	}
	require.NotNil(t, vkPolicy, "Valkey NetworkPolicy must exist")

	spec, _ := vkPolicy["spec"].(map[string]any)
	ingress, _ := spec["ingress"].([]any)

	var foundMigrateRule bool
	for _, rule := range ingress {
		rm, _ := rule.(map[string]any)
		from, _ := rm["from"].([]any)
		for _, f := range from {
			fm, _ := f.(map[string]any)
			podSel, _ := fm["podSelector"].(map[string]any)
			ml, _ := podSel["matchLabels"].(map[string]any)
			if comp, _ := ml["app.kubernetes.io/component"].(string); comp == "migrate" {
				foundMigrateRule = true
			}
		}
	}
	require.True(t, foundMigrateRule,
		"Valkey NetworkPolicy must allow the migrate Job pod selector (F8 fix: was missing)")
}

// =============================================================================
// Helm audit — additional depth tests (gap analysis follow-up)
//
// The initial TestF1–TestF8 suite verified the fixes at a coarse level.
// These tests close the specific gaps identified in the gap analysis:
//   - F2: probe thresholds (not just type)
//   - F3: non-zero UID; /tmp emptyDir declared AND mounted
//   - F4: volumeMounts wired into the frontend container (not just declared)
//   - F5: primary host also has /api path; TLS entry for additionalHost
//   - F8: API-allow rule still present after adding the migrate rule
//   - Negative: MCP disabled → no Deployment/Service rendered
// =============================================================================

// volumeMountPaths returns the set of mountPath values for a container.
func volumeMountPaths(c map[string]any) map[string]bool {
	out := map[string]bool{}
	mounts, _ := c["volumeMounts"].([]any)
	for _, m := range mounts {
		mm, _ := m.(map[string]any)
		if mp, ok := mm["mountPath"].(string); ok {
			out[mp] = true
		}
	}
	return out
}

// TestF2_MCPProbeThresholds guards probe timing so a revert to the old
// config (5s initial delay, 30s period, no readiness) is caught.
func TestF2_MCPProbeThresholds(t *testing.T) {
	docs := helmTemplate(t, "mcp:\n  enabled: true\n")
	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.NotNil(t, deploy)
	c := containerByName(deploy, "mcp")
	require.NotNil(t, c)

	liveness, _ := c["livenessProbe"].(map[string]any)
	require.NotNil(t, liveness)
	require.EqualValues(t, 5, liveness["initialDelaySeconds"],
		"MCP liveness initialDelaySeconds must be 5")
	require.EqualValues(t, 30, liveness["periodSeconds"],
		"MCP liveness periodSeconds must be 30")

	readiness, _ := c["readinessProbe"].(map[string]any)
	require.NotNil(t, readiness)
	require.EqualValues(t, 3, readiness["initialDelaySeconds"],
		"MCP readiness initialDelaySeconds must be 3")
	require.EqualValues(t, 10, readiness["periodSeconds"],
		"MCP readiness periodSeconds must be 10")
}

// TestF3_MCPNonZeroUID guards that the MCP pod runs as a non-zero UID
// (65532). runAsNonRoot=true alone is not sufficient — some runtimes accept
// numeric UID 0 and rely on the admission webhook to block it.
func TestF3_MCPNonZeroUID(t *testing.T) {
	docs := helmTemplate(t, "mcp:\n  enabled: true\n")
	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.NotNil(t, deploy)

	psc := podSecCtx(deploy)
	require.NotNil(t, psc)
	uid := psc["runAsUser"]
	require.NotNil(t, uid, "MCP podSecurityContext must set runAsUser")
	require.NotEqual(t, float64(0), uid,
		"MCP podSecurityContext.runAsUser must not be 0 (root)")
}

// TestF3_MCPTmpVolumeAndMount guards that the /tmp emptyDir is both declared
// as a volume AND mounted into the mcp container. A regression could add the
// volume but forget the mount (or vice versa), causing readOnlyRootFilesystem
// to reject any write to /tmp at runtime.
func TestF3_MCPTmpVolumeAndMount(t *testing.T) {
	docs := helmTemplate(t, "mcp:\n  enabled: true\n")
	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.NotNil(t, deploy)

	// Check volume declared at pod spec level.
	spec, _ := deploy["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	volumes, _ := podSpec["volumes"].([]any)
	var hasTmpVolume bool
	for _, v := range volumes {
		vm, _ := v.(map[string]any)
		if n, _ := vm["name"].(string); n == "tmp" {
			_, isEmptyDir := vm["emptyDir"]
			if isEmptyDir {
				hasTmpVolume = true
			}
		}
	}
	require.True(t, hasTmpVolume,
		"MCP pod must declare a 'tmp' emptyDir volume (F3 fix: readOnlyRootFilesystem=true requires writable /tmp)")

	// Check mount wired into the container.
	c := containerByName(deploy, "mcp")
	require.NotNil(t, c)
	mounts := volumeMountPaths(c)
	require.True(t, mounts["/tmp"],
		"MCP container must have a volumeMount for /tmp (F3 fix)")
}

// TestF4_FrontendVolumeMountsWired guards that the three emptyDir volumes
// (nginx-cache, nginx-run, tmp) are not just declared but actually wired
// into the frontend container at the correct paths. A regression could add
// the volumes without the mounts, leaving nginx unable to write and crashing
// on startup with readOnlyRootFilesystem=true.
func TestF4_FrontendVolumeMountsWired(t *testing.T) {
	docs := helmTemplate(t, "frontend:\n  enabled: true\n")
	deploy := findDeploymentByNameSubstr(docs, "-frontend")
	require.NotNil(t, deploy)

	c := containerByName(deploy, "frontend")
	require.NotNil(t, c)
	mounts := volumeMountPaths(c)

	for path, desc := range map[string]string{
		"/var/cache/nginx": "nginx cache dir (F4 fix)",
		"/var/run":         "nginx pid/socket dir (F4 fix)",
		"/tmp":             "tmp dir (F4 fix)",
	} {
		require.True(t, mounts[path],
			"frontend container must have volumeMount at %s — %s", path, desc)
	}
}

// TestF5_PrimaryHostHasAPIPath guards the primary host rule in the frontend
// Ingress. A refactor that broke only the primary host while keeping
// additionalHosts intact would not be caught by TestF5 alone.
func TestF5_PrimaryHostHasAPIPath(t *testing.T) {
	docs := helmTemplate(t, `frontend:
  enabled: true
  ingress:
    enabled: true
    host: "primary.example.com"
`)
	var frontendIngress map[string]any
	for _, d := range docs {
		if d["kind"] == "Ingress" && strings.Contains(metaName(d), "frontend") {
			frontendIngress = d
			break
		}
	}
	require.NotNil(t, frontendIngress)

	spec, _ := frontendIngress["spec"].(map[string]any)
	rules, _ := spec["rules"].([]any)
	var primaryRule map[string]any
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if h, _ := rm["host"].(string); h == "primary.example.com" {
			primaryRule = rm
			break
		}
	}
	require.NotNil(t, primaryRule, "primary host rule must exist")

	http, _ := primaryRule["http"].(map[string]any)
	paths, _ := http["paths"].([]any)
	var hasAPI, hasRoot bool
	for _, p := range paths {
		pm, _ := p.(map[string]any)
		switch pm["path"] {
		case "/api":
			hasAPI = true
		case "/":
			hasRoot = true
		}
	}
	require.True(t, hasAPI, "primary host must have /api path to API service")
	require.True(t, hasRoot, "primary host must have / path to frontend service")
}

// TestF5_AdditionalHostsTLSEntry guards that when tls=true, the additionalHost
// gets its own TLS entry in the Ingress spec. Without it, HTTPS terminates
// with the primary host's certificate (wrong cert for the SNI name).
func TestF5_AdditionalHostsTLSEntry(t *testing.T) {
	docs := helmTemplate(t, `frontend:
  enabled: true
  ingress:
    enabled: true
    host: "primary.example.com"
    tls: true
    tlsSecret: "primary-tls"
    additionalHosts:
      - host: "extra.example.com"
        tlsSecret: "extra-tls"
`)
	var frontendIngress map[string]any
	for _, d := range docs {
		if d["kind"] == "Ingress" && strings.Contains(metaName(d), "frontend") {
			frontendIngress = d
			break
		}
	}
	require.NotNil(t, frontendIngress)

	spec, _ := frontendIngress["spec"].(map[string]any)
	tls, _ := spec["tls"].([]any)
	require.NotEmpty(t, tls, "tls block must be present when frontend.ingress.tls=true")

	var foundExtraTLS bool
	for _, t := range tls {
		tm, _ := t.(map[string]any)
		hosts, _ := tm["hosts"].([]any)
		for _, h := range hosts {
			if h == "extra.example.com" {
				foundExtraTLS = true
			}
		}
	}
	require.True(t, foundExtraTLS,
		"additionalHost extra.example.com must have a TLS entry (F5 fix)")
}

// TestF8_ValkeyAPIAllowRulePreserved guards that the existing API pod allow
// rule in the Valkey policy was not accidentally removed when the migrate
// rule was added. A regression that replaced rather than appended would
// break Valkey cache for the API.
func TestF8_ValkeyAPIAllowRulePreserved(t *testing.T) {
	docs := helmTemplate(t, "")
	policies := findByKind(docs, "NetworkPolicy")

	var vkPolicy map[string]any
	for _, p := range policies {
		spec, _ := p["spec"].(map[string]any)
		sel, _ := spec["podSelector"].(map[string]any)
		ml, _ := sel["matchLabels"].(map[string]any)
		if app, _ := ml["app"].(string); app == "valkey" {
			vkPolicy = p
			break
		}
	}
	require.NotNil(t, vkPolicy)

	spec, _ := vkPolicy["spec"].(map[string]any)
	ingress, _ := spec["ingress"].([]any)
	require.GreaterOrEqual(t, len(ingress), 2,
		"Valkey NetworkPolicy must have at least 2 ingress rules (API + migrate)")

	var foundAPIRule bool
	for _, rule := range ingress {
		rm, _ := rule.(map[string]any)
		from, _ := rm["from"].([]any)
		for _, f := range from {
			fm, _ := f.(map[string]any)
			podSel, _ := fm["podSelector"].(map[string]any)
			ml, _ := podSel["matchLabels"].(map[string]any)
			if comp, _ := ml["app.kubernetes.io/component"].(string); comp == "api" {
				foundAPIRule = true
			}
		}
	}
	require.True(t, foundAPIRule,
		"Valkey NetworkPolicy must still allow the API pod (F8 fix must not have removed it)")
}

// TestF_MCPDisabled_NoResourcesRendered guards that when mcp.enabled=false
// (the chart default), no MCP Deployment or Service is rendered. If the
// gating condition is accidentally removed, every install would ship an
// MCP pod even when the operator didn't want one.
func TestF_MCPDisabled_NoResourcesRendered(t *testing.T) {
	// Explicitly disable to verify the default behavior is honored.
	docs := helmTemplate(t, "mcp:\n  enabled: false\n")

	deploy := findDeploymentByNameSubstr(docs, "-mcp")
	require.Nil(t, deploy,
		"no MCP Deployment must be rendered when mcp.enabled=false")

	svc := findServiceByNameSubstr(docs, "-mcp")
	require.Nil(t, svc,
		"no MCP Service must be rendered when mcp.enabled=false")
}

// TestF133_ControllerSecretsAreNamespaceScoped asserts that secrets
// and pods are NEVER granted CRUD verbs via ClusterRole, even when
// rbac.scope=cluster. Read-only verbs (get/list/watch) are
// permitted because the controller-runtime informer cache requires
// cluster-wide watches; CRUD is the dangerous surface (F1.3.3 / G5).
func TestF133_ControllerSecretsAreNamespaceScoped(t *testing.T) {
	docs := helmTemplate(t, "rbac:\n  scope: cluster\n")
	clusterRoles := findResources(docs, "ClusterRole")

	// CRUD verbs that MUST NOT appear cluster-wide on secrets/pods.
	mutatingVerbs := map[string]struct{}{
		"create": {}, "update": {}, "patch": {}, "delete": {}, "deletecollection": {},
	}

	for _, cr := range clusterRoles {
		// Walk rules; for any rule that grants secrets or pods, the
		// verb set must contain only read-only verbs.
		rules, _ := cr["rules"].([]any)
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			groups, _ := rule["apiGroups"].([]any)
			resources, _ := rule["resources"].([]any)
			verbs, _ := rule["verbs"].([]any)

			coreGroup := false
			for _, g := range groups {
				if s, ok := g.(string); ok && s == "" {
					coreGroup = true
				}
			}
			if !coreGroup {
				continue
			}
			for _, res := range resources {
				resStr, _ := res.(string)
				if resStr != "secrets" && resStr != "pods" {
					continue
				}
				for _, v := range verbs {
					verbStr, _ := v.(string)
					if _, isMutating := mutatingVerbs[verbStr]; isMutating {
						t.Fatalf(
							"ClusterRole %q grants cluster-wide %q on %s — must be namespace-scoped (F1.3.3 / G5)",
							metaName(cr), verbStr, resStr)
					}
				}
			}
		}
	}
}

// =============================================================================
// InferenceRelay — API ClusterRole for cluster-scoped CRD
// =============================================================================

// TestRelay_APIInferenceRelayClusterRole_DisabledByDefault asserts that
// NEITHER the API ClusterRole nor its ClusterRoleBinding for inferencerelays
// renders when the relay subsystem is disabled (the chart default). Guards
// against accidental removal of the {{- if }} gate on either document.
func TestRelay_APIInferenceRelayClusterRole_DisabledByDefault(t *testing.T) {
	docs := helmTemplate(t, "")
	for _, d := range docs {
		k, _ := d["kind"].(string)
		if k != "ClusterRole" && k != "ClusterRoleBinding" {
			continue
		}
		require.NotContains(t, metaName(d), "api-inferencerelay",
			"API InferenceRelay %s must NOT render when controller.inferenceRelay.enabled is false (default)", k)
	}
}

// TestRelay_APIInferenceRelayClusterRole_RendersWhenEnabled asserts the
// API ClusterRole + ClusterRoleBinding for inferencerelays render with a
// least-privilege grant when the relay subsystem is enabled, and that the
// binding is correctly wired (roleRef → the ClusterRole, subject → the API
// ServiceAccount in the release namespace). The InferenceRelay CRD is
// cluster-scoped, so a namespace Role is insufficient.
func TestRelay_APIInferenceRelayClusterRole_RendersWhenEnabled(t *testing.T) {
	docs := helmTemplate(t, "controller:\n  inferenceRelay:\n    enabled: true\n")

	leastPrivilege := []string{"get", "list", "create", "update"}

	var roleName, bindingRoleRef string
	var sawRole, sawBinding bool
	for _, d := range docs {
		k, _ := d["kind"].(string)
		name := metaName(d)
		if !strings.Contains(name, "api-inferencerelay") {
			continue
		}
		switch k {
		case "ClusterRole":
			sawRole = true
			roleName = name
			rv := resourceVerbs(d)
			verbs := rv["llmsafespaces.dev/inferencerelays"]
			require.NotEmpty(t, verbs,
				"ClusterRole %q must grant access to inferencerelays", name)
			require.ElementsMatch(t, leastPrivilege, verbs,
				"API inferencerelays grant must be exactly [get,list,create,update] (least-privilege)")
			require.NotContains(t, rv, "llmsafespaces.dev/inferencerelays/status",
				"API must NOT receive /status subresource access")
			require.NotContains(t, rv, "llmsafespaces.dev/inferencerelays/finalizers",
				"API must NOT receive /finalizers subresource access")
		case "ClusterRoleBinding":
			sawBinding = true
			roleRef, _ := d["roleRef"].(map[string]any)
			require.Equal(t, "ClusterRole", roleRef["kind"],
				"ClusterRoleBinding %q roleRef.kind must be ClusterRole", name)
			roleRefName, _ := roleRef["name"].(string)
			require.NotEmpty(t, roleRefName,
				"ClusterRoleBinding %q must reference a ClusterRole by name", name)
			bindingRoleRef = roleRefName
			subjects, _ := d["subjects"].([]any)
			require.Len(t, subjects, 1,
				"ClusterRoleBinding %q must bind exactly one subject (the API ServiceAccount)", name)
			subj, _ := subjects[0].(map[string]any)
			require.Equal(t, "ServiceAccount", subj["kind"],
				"ClusterRoleBinding %q subject must be a ServiceAccount", name)
			subjNS, _ := subj["namespace"].(string)
			require.Equal(t, "test-ns", subjNS,
				"ClusterRoleBinding %q subject must be in the release namespace", name)
		}
	}
	require.True(t, sawRole,
		"ClusterRole for API inferencerelays must render when controller.inferenceRelay.enabled=true")
	require.True(t, sawBinding,
		"ClusterRoleBinding for API inferencerelays must render when controller.inferenceRelay.enabled=true")
	require.Equal(t, roleName, bindingRoleRef,
		"ClusterRoleBinding.roleRef.name must point at the rendered API inferencerelay ClusterRole")
}

// =============================================================================
// InferenceRelay — US-42.8 router WireGuard sidecar + network-agnostic ingress
// =============================================================================
//
// These tests guard the US-42.8 chart implementation (worklog 0362 redesign):
// the relay-router Deployment gains a WireGuard sidecar that brings up wg0
// (10.42.42.1/24), and a second Service template renders the WireGuard UDP
// endpoint in one of four operator-selectable ingress modes. The chart NEVER
// installs MetalLB. Default mode is `external` (no WG Service at all).

// relayEnabledValues is the minimal values that enable the relay-router
// subsystem (it is disabled by default).
const relayEnabledValues = "controller:\n  inferenceRelay:\n    enabled: true\n"

// podSpecMap returns the .spec.template.spec (PodSpec) of a Deployment doc.
func podSpecMap(deploy map[string]any) map[string]any {
	spec, _ := deploy["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	ps, _ := tmpl["spec"].(map[string]any)
	return ps
}

// capSet returns the capabilities.add list (as a string slice) of a container.
func capSet(c map[string]any) []string {
	sc, _ := c["securityContext"].(map[string]any)
	caps, _ := sc["capabilities"].(map[string]any)
	add, _ := caps["add"].([]any)
	out := make([]string, 0, len(add))
	for _, a := range add {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// toInt coerces a YAML-decoded number to int. sigs.k8s.io/yaml unmarshals
// numeric scalars as float64 (via encoding/json), so a bare .(int) assertion
// silently returns 0 — this helper handles int / int64 / float64.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// hasWGUDPPort reports whether a Service doc exposes a UDP port on 51820
// (the WireGuard endpoint). This is what distinguishes the WG Service from
// the always-present TCP 8080 ClusterIP Service.
func hasWGUDPPort(svc map[string]any) bool {
	spec, _ := svc["spec"].(map[string]any)
	ports, _ := spec["ports"].([]any)
	for _, p := range ports {
		pm, _ := p.(map[string]any)
		port := toInt(pm["port"])
		proto, _ := pm["protocol"].(string)
		if port == 51820 && (proto == "UDP" || proto == "") {
			return true
		}
	}
	return false
}

// svcType returns spec.type of a Service doc.
func svcType(svc map[string]any) string {
	spec, _ := svc["spec"].(map[string]any)
	t, _ := spec["type"].(string)
	return t
}

// TestRelayRouter_WGSidecar_HiddenWhenInferenceRelayDisabled asserts the
// relay-router Deployment (and thus its WG sidecar) does NOT render when
// controller.inferenceRelay.enabled is false (the chart default). Preserves
// the existing gated behavior from worklog 0299.
func TestRelayRouter_WGSidecar_HiddenWhenInferenceRelayDisabled(t *testing.T) {
	docs := helmTemplate(t, "")
	require.Nil(t, findDeploymentByNameSubstr(docs, "relay-router"),
		"relay-router Deployment must NOT render when controller.inferenceRelay.enabled is false (default)")
}

// TestRelayRouter_WGSidecar_RendersWhenInferenceRelayEnabled asserts the
// relay-router Deployment renders with a WireGuard sidecar container when
// the relay subsystem is enabled. The sidecar must carry NET_ADMIN + NET_RAW
// (the minimum to create/manage the wg0 interface) and mount the
// controller-managed relay-router-wg Secret (router private key).
func TestRelayRouter_WGSidecar_RendersWhenInferenceRelayEnabled(t *testing.T) {
	docs := helmTemplate(t, relayEnabledValues)

	deploy := findDeploymentByNameSubstr(docs, "relay-router")
	require.NotNil(t, deploy, "relay-router Deployment must render when inferenceRelay is enabled")

	wg := containerByName(deploy, "wireguard")
	require.NotNil(t, wg, "relay-router pod must have a 'wireguard' sidecar container")
	require.Contains(t, capSet(wg), "NET_ADMIN",
		"wireguard sidecar needs NET_ADMIN to create/manage wg0")
	require.Contains(t, capSet(wg), "NET_RAW",
		"wireguard sidecar needs NET_RAW for raw-socket operations")

	// The router private key lives in the controller-managed relay-router-wg
	// Secret (controller/internal/relay/reconciler.go ensureRouterWGKey). The
	// sidecar must mount it.
	mounts, _ := wg["volumeMounts"].([]any)
	var mountedSecret bool
	for _, m := range mounts {
		mm, _ := m.(map[string]any)
		if mm["mountPath"] == "/etc/wireguard/secret" {
			mountedSecret = true
		}
	}
	require.True(t, mountedSecret,
		"wireguard sidecar must mount the relay-router-wg Secret at /etc/wireguard/secret")

	// The main router container must remain non-root with ALL caps dropped —
	// the WG sidecar absorbs the privileged work, the router does not.
	router := containerByName(deploy, "relay-router")
	require.NotNil(t, router, "relay-router pod must keep its 'relay-router' main container")
	require.Empty(t, capSet(router),
		"relay-router main container must drop ALL capabilities (WG sidecar owns net admin)")

	// The pod-level securityContext must not force runAsNonRoot, otherwise the
	// root WG sidecar (runAsUser: 0) is rejected by the kubelet. Non-root is
	// enforced at the router container instead.
	psc := podSecCtx(deploy)
	if _, ok := psc["runAsNonRoot"]; ok {
		require.False(t, psc["runAsNonRoot"].(bool),
			"pod-level runAsNonRoot must be false so the root WG sidecar can start; the router container enforces non-root itself")
	}
}

// TestRelayRouter_WGIngress_ExternalMode_RendersNoUDPService asserts that the
// default ingress mode (`external`) renders NO WireGuard Service — the
// operator wires UDP 51820 to the router pod out-of-band. Only the existing
// TCP 8080 ClusterIP Service should be present. This guarantees `helm
// install` succeeds on any K8s distribution, including ones with no LB
// controller.
func TestRelayRouter_WGIngress_ExternalMode_RendersNoUDPService(t *testing.T) {
	docs := helmTemplate(t, relayEnabledValues)

	for _, d := range findByKind(docs, "Service") {
		if strings.Contains(metaName(d), "relay-router") {
			require.False(t, hasWGUDPPort(d),
				"mode=external must render no WG UDP Service (found UDP 51820 on %s)", metaName(d))
		}
	}
}

// TestRelayRouter_WGIngress_LoadBalancerMode_RendersLBService asserts that
// mode=loadBalancer renders exactly one Service of type LoadBalancer on UDP
// 51820, and that loadBalancerIP / loadBalancerClass / annotations are
// propagated when set.
func TestRelayRouter_WGIngress_LoadBalancerMode_RendersLBService(t *testing.T) {
	vals := `controller:
  inferenceRelay:
    enabled: true
    router:
      wireGuard:
        ingress:
          mode: loadBalancer
          loadBalancerIP: "203.0.113.10"
          loadBalancerClass: "metallb"
          annotations:
            metallb.io/address-pool: "relay-pool"
`
	docs := helmTemplate(t, vals)

	var wgSvc map[string]any
	for _, d := range findByKind(docs, "Service") {
		if strings.Contains(metaName(d), "relay-router") && hasWGUDPPort(d) {
			wgSvc = d
			break
		}
	}
	require.NotNil(t, wgSvc, "mode=loadBalancer must render a WG Service")
	require.Equal(t, "LoadBalancer", svcType(wgSvc),
		"mode=loadBalancer WG Service must be type LoadBalancer")

	spec, _ := wgSvc["spec"].(map[string]any)
	require.Equal(t, "203.0.113.10", spec["loadBalancerIP"],
		"loadBalancerIP must propagate to the WG Service")
	require.Equal(t, "metallb", spec["loadBalancerClass"],
		"loadBalancerClass must propagate to the WG Service")

	meta, _ := wgSvc["metadata"].(map[string]any)
	ann, _ := meta["annotations"].(map[string]any)
	require.Equal(t, "relay-pool", ann["metallb.io/address-pool"],
		"WG Service annotations must propagate")

	// Exactly one WG Service (the LB), in addition to the TCP 8080 ClusterIP.
	count := 0
	for _, d := range findByKind(docs, "Service") {
		if strings.Contains(metaName(d), "relay-router") && hasWGUDPPort(d) {
			count++
		}
	}
	require.Equal(t, 1, count, "exactly one WG UDP Service expected in loadBalancer mode")
}

// TestRelayRouter_WGIngress_NodePortMode_RendersNodePortService asserts that
// mode=nodePort renders a Service of type NodePort on UDP 51820 with the
// pinned nodePort.
func TestRelayRouter_WGIngress_NodePortMode_RendersNodePortService(t *testing.T) {
	vals := `controller:
  inferenceRelay:
    enabled: true
    router:
      wireGuard:
        ingress:
          mode: nodePort
          nodePort: 31820
`
	docs := helmTemplate(t, vals)

	var wgSvc map[string]any
	for _, d := range findByKind(docs, "Service") {
		if strings.Contains(metaName(d), "relay-router") && hasWGUDPPort(d) {
			wgSvc = d
			break
		}
	}
	require.NotNil(t, wgSvc, "mode=nodePort must render a WG Service")
	require.Equal(t, "NodePort", svcType(wgSvc),
		"mode=nodePort WG Service must be type NodePort")

	spec, _ := wgSvc["spec"].(map[string]any)
	ports, _ := spec["ports"].([]any)
	var np any
	for _, p := range ports {
		pm, _ := p.(map[string]any)
		if toInt(pm["port"]) == 51820 {
			np = pm["nodePort"]
		}
	}
	require.Equal(t, 31820, toInt(np),
		"mode=nodePort WG Service must pin nodePort to 31820 for stable DNS")
}

// TestRelayRouter_WGIngress_HostNetworkMode_RendersHostNetworkPod asserts
// that mode=hostNetwork sets pod.spec.hostNetwork: true, applies a
// nodeSelector keyed on the operator-applied label, and renders NO WG Service
// (the router is dialed directly by node IP).
func TestRelayRouter_WGIngress_HostNetworkMode_RendersHostNetworkPod(t *testing.T) {
	vals := `controller:
  inferenceRelay:
    enabled: true
    router:
      wireGuard:
        ingress:
          mode: hostNetwork
`
	docs := helmTemplate(t, vals)

	deploy := findDeploymentByNameSubstr(docs, "relay-router")
	require.NotNil(t, deploy)

	ps := podSpecMap(deploy)
	hn, _ := ps["hostNetwork"].(bool)
	require.True(t, hn,
		"mode=hostNetwork must set pod.spec.hostNetwork: true")

	sel, _ := ps["nodeSelector"].(map[string]any)
	require.Equal(t, "true", sel["llmsafespaces.dev/relay-router"],
		"mode=hostNetwork must select the operator-labeled node via llmsafespaces.dev/relay-router=true")

	for _, d := range findByKind(docs, "Service") {
		if strings.Contains(metaName(d), "relay-router") {
			require.False(t, hasWGUDPPort(d),
				"mode=hostNetwork must render no WG Service (router is on hostNetwork, dialed by node IP)")
		}
	}
}

// TestRelayRouter_NetworkPolicy_RendersWhenEnabled asserts that enabling the
// relay subsystem renders a NetworkPolicy that (a) selects the relay-router
// pod, (b) allows TCP 8080 ingress from workspace pods (the proxy path) and
// the controller pod (metrics scrape), (c) allows UDP 51820 ingress from
// anywhere (relay VMs dial in from public cloud IPs — WG key-pinning is the
// auth), and (d) allows unrestricted egress (tunnel + DNS + Zen-direct
// fallback).
func TestRelayRouter_NetworkPolicy_RendersWhenEnabled(t *testing.T) {
	docs := helmTemplate(t, relayEnabledValues)

	var policy map[string]any
	for _, d := range findByKind(docs, "NetworkPolicy") {
		if strings.Contains(metaName(d), "relay-router") {
			policy = d
			break
		}
	}
	require.NotNil(t, policy, "relay-router NetworkPolicy must render when inferenceRelay is enabled")

	spec, _ := policy["spec"].(map[string]any)
	sel, _ := spec["podSelector"].(map[string]any)
	matchLabels, _ := sel["matchLabels"].(map[string]any)
	require.Equal(t, "relay-router", matchLabels["app.kubernetes.io/component"],
		"relay-router NetworkPolicy must select relay-router pods")

	types, _ := spec["policyTypes"].([]any)
	require.Contains(t, types, "Ingress", "policy must govern ingress")
	require.Contains(t, types, "Egress", "policy must govern egress")

	ingress, _ := spec["ingress"].([]any)
	var saw8080, saw51820Any bool
	for _, rule := range ingress {
		rm, _ := rule.(map[string]any)
		ports, _ := rm["ports"].([]any)
		for _, p := range ports {
			pm, _ := p.(map[string]any)
			port := toInt(pm["port"])
			proto, _ := pm["protocol"].(string)
			if port == 8080 {
				saw8080 = true
			}
			if port == 51820 && proto == "UDP" {
				// "Allow from anywhere" means the rule has NO `from` key
				// (an absent/empty from permits all sources). relay VMs
				// connect from public cloud IPs that are not knowable ahead
				// of time — WG key-pinning is the auth.
				_, hasFrom := rm["from"]
				if !hasFrom {
					saw51820Any = true
				}
			}
		}
	}
	require.True(t, saw8080, "NetworkPolicy must allow TCP 8080 ingress (workspace proxy + controller metrics)")
	require.True(t, saw51820Any, "NetworkPolicy must allow UDP 51820 ingress from anywhere (relay VMs dial in from public IPs)")

	// Egress must be effectively unrestricted for the router to reach
	// 10.42.42.x over the tunnel, DNS, and Zen-direct fallback.
	egress, _ := spec["egress"].([]any)
	require.NotEmpty(t, egress, "NetworkPolicy must include egress rules")
}

// =============================================================================
// Monitoring — Grafana dashboards, PrometheusRule, ServiceMonitor
// =============================================================================

// TestMonitoring_DisabledByDefault_NoResourcesRendered verifies the master
// toggle defaults to false and no monitoring resources are rendered.
func TestMonitoring_DisabledByDefault_NoResourcesRendered(t *testing.T) {
	docs := helmTemplate(t, "")
	for _, d := range docs {
		k, _ := d["kind"].(string)
		require.NotEqual(t, "PrometheusRule", k,
			"PrometheusRule must NOT render when monitoring.enabled is false (default)")
	}
	for _, d := range docs {
		meta, _ := d["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		require.False(t, strings.Contains(name, "grafana-dashboards"),
			"dashboard ConfigMap must NOT render when monitoring is disabled")
	}
}

// TestMonitoring_Enabled_RendersAllResources verifies all monitoring resources
// appear when the master toggle is on.
func TestMonitoring_Enabled_RendersAllResources(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")

	var sawDashboards, sawPrometheusRule, sawAPIServMon, sawCtrlServMon bool
	for _, d := range docs {
		k, _ := d["kind"].(string)
		meta, _ := d["metadata"].(map[string]any)
		name, _ := meta["name"].(string)

		if k == "ConfigMap" && strings.Contains(name, "grafana-dashboards") {
			sawDashboards = true
		}
		if k == "PrometheusRule" {
			sawPrometheusRule = true
		}
		if k == "ServiceMonitor" && strings.Contains(name, "-api") {
			sawAPIServMon = true
		}
		if k == "ServiceMonitor" && strings.Contains(name, "-controller") {
			sawCtrlServMon = true
		}
	}
	require.True(t, sawDashboards, "dashboard ConfigMap must render")
	require.True(t, sawPrometheusRule, "PrometheusRule must render")
	require.True(t, sawAPIServMon, "API ServiceMonitor must render")
	require.True(t, sawCtrlServMon, "controller ServiceMonitor must render")
}

// TestMonitoring_DashboardsDisabled_NoConfigMap verifies the sub-toggle
// can independently disable dashboards while keeping alerts and monitors.
func TestMonitoring_DashboardsDisabled_NoConfigMap(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n  dashboards:\n    enabled: false\n")
	for _, d := range docs {
		meta, _ := d["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		require.False(t, strings.Contains(name, "grafana-dashboards"),
			"dashboard ConfigMap must NOT render when dashboards.enabled=false")
	}
	var sawPrometheusRule bool
	for _, d := range docs {
		if d["kind"] == "PrometheusRule" {
			sawPrometheusRule = true
		}
	}
	require.True(t, sawPrometheusRule, "PrometheusRule must still render when only dashboards disabled")
}

// TestMonitoring_ServiceMonitorsDisabled_NoServiceMonitors verifies the
// sub-toggle can independently disable ServiceMonitors.
func TestMonitoring_ServiceMonitorsDisabled_NoServiceMonitors(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n  serviceMonitors:\n    enabled: false\n")
	for _, d := range docs {
		require.NotEqual(t, "ServiceMonitor", d["kind"],
			"ServiceMonitor must NOT render when serviceMonitors.enabled=false")
	}
}

// TestMonitoring_ControllerMetricsAddrOverride verifies the controller
// deployment uses 0.0.0.0:8080 for metrics when ServiceMonitors are enabled.
func TestMonitoring_ControllerMetricsAddrOverride(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	args := findControllerArgs(t, docs)
	require.NotEmpty(t, args)
	var found string
	for _, a := range args {
		if strings.HasPrefix(a, "--metrics-addr=") {
			found = a
			break
		}
	}
	require.Equal(t, "--metrics-addr=0.0.0.0:8080", found,
		"controller must override metricsAddr to 0.0.0.0:8080 when ServiceMonitors enabled")
}

// TestMonitoring_ControllerMetricsAddrDefault_NoOverride verifies the
// controller keeps loopback binding when monitoring is off.
func TestMonitoring_ControllerMetricsAddrDefault_NoOverride(t *testing.T) {
	docs := helmTemplate(t, "")
	args := findControllerArgs(t, docs)
	require.NotEmpty(t, args)
	var found string
	for _, a := range args {
		if strings.HasPrefix(a, "--metrics-addr=") {
			found = a
			break
		}
	}
	require.Equal(t, "--metrics-addr=127.0.0.1:8080", found,
		"controller must keep default loopback binding when monitoring is off")
}

// TestMonitoring_PrometheusRulesDisabled_NoRules verifies the sub-toggle
// can independently disable PrometheusRules.
func TestMonitoring_PrometheusRulesDisabled_NoRules(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n  prometheusRules:\n    enabled: false\n")
	for _, d := range docs {
		require.NotEqual(t, "PrometheusRule", d["kind"],
			"PrometheusRule must NOT render when prometheusRules.enabled=false")
	}
}

// TestMonitoring_DashboardConfigMap_ContainsJSON verifies the dashboard
// ConfigMap data keys include the expected dashboard files.
func TestMonitoring_DashboardConfigMap_ContainsJSON(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	var cm map[string]any
	for _, d := range docs {
		if d["kind"] == "ConfigMap" {
			_, _ = d["metadata"].(map[string]any)
			if strings.Contains(metaName(d), "grafana-dashboards") {
				cm = d
				break
			}
		}
	}
	require.NotNil(t, cm, "dashboard ConfigMap must exist")
	data, _ := cm["data"].(map[string]any)
	require.Contains(t, data, "operational.json", "ConfigMap must contain operational.json")
	require.Contains(t, data, "billing.json", "ConfigMap must contain billing.json")
}

// TestMonitoring_DashboardConfigMap_HasGrafanaLabel verifies the
// grafana_dashboard label is present for the sidecar importer.
func TestMonitoring_DashboardConfigMap_HasGrafanaLabel(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	var cm map[string]any
	for _, d := range docs {
		if d["kind"] == "ConfigMap" {
			if strings.Contains(metaName(d), "grafana-dashboards") {
				cm = d
				break
			}
		}
	}
	require.NotNil(t, cm)
	meta, _ := cm["metadata"].(map[string]any)
	labels, _ := meta["labels"].(map[string]any)
	require.Equal(t, "1", labels["grafana_dashboard"],
		"dashboard ConfigMap must have grafana_dashboard=1 label for sidecar import")
}

// TestMonitoring_NamespaceOverride verifies all monitoring resources respect
// the namespace override.
func TestMonitoring_NamespaceOverride(t *testing.T) {
	docs := helmTemplate(t, `monitoring:
  enabled: true
  dashboards:
    namespace: monitoring
  prometheusRules:
    namespace: monitoring
  serviceMonitors:
    namespace: monitoring
`)
	for _, d := range docs {
		k, _ := d["kind"].(string)
		if k == "PrometheusRule" || k == "ServiceMonitor" {
			meta, _ := d["metadata"].(map[string]any)
			ns, _ := meta["namespace"].(string)
			require.Equal(t, "monitoring", ns,
				"%s namespace must match override", k)
		}
		if k == "ConfigMap" {
			meta, _ := d["metadata"].(map[string]any)
			name, _ := meta["name"].(string)
			if strings.Contains(name, "grafana-dashboards") {
				ns, _ := meta["namespace"].(string)
				require.Equal(t, "monitoring", ns,
					"dashboard ConfigMap namespace must match override")
			}
		}
	}
}

// TestMonitoring_PrometheusRule_SpecIsTopLevel verifies that the rendered
// PrometheusRule has spec.groups as a top-level key. This is a regression
// test for an accidental indentation bug that nested `spec:` under
// `metadata:`, silently breaking all alerting rules.
func TestMonitoring_PrometheusRule_SpecIsTopLevel(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	var rule map[string]any
	for _, d := range docs {
		if d["kind"] == "PrometheusRule" {
			rule = d
			break
		}
	}
	require.NotNil(t, rule, "PrometheusRule must be rendered")

	spec, ok := rule["spec"].(map[string]any)
	require.True(t, ok,
		"PrometheusRule must have a top-level spec key (not nested under metadata)")
	groups, ok := spec["groups"].([]any)
	require.True(t, ok,
		"PrometheusRule spec must have a groups array")
	require.NotEmpty(t, groups,
		"PrometheusRule spec.groups must not be empty")
}

// TestMonitoring_PrometheusRule_ContainsAllAlerts verifies all expected
// alert names are present in the rendered PrometheusRule.
func TestMonitoring_PrometheusRule_ContainsAllAlerts(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	var rule map[string]any
	for _, d := range docs {
		if d["kind"] == "PrometheusRule" {
			rule = d
			break
		}
	}
	require.NotNil(t, rule, "PrometheusRule must be rendered")

	spec, ok := rule["spec"].(map[string]any)
	require.True(t, ok, "spec must be top-level")
	groups, _ := spec["groups"].([]any)

	alertNames := map[string]bool{}
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		rules, _ := gm["rules"].([]any)
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			if name, ok := rm["alert"].(string); ok {
				alertNames[name] = true
			}
		}
	}

	expected := []string{
		"LLMSafeSpacesLowAvailability",
		"LLMSafeSpacesHighLatency",
		"LLMSafeSpacesHighAuthFailures",
		"LLMSafeSpacesSSEBrokerDroppingEvents",
		"LLMSafeSpacesReconciliationErrors",
		"LLMSafeSpacesWorkspaceFailures",
		"LLMSafeSpacesWorkspaceCreationSlow",
		"LLMSafeSpacesRecoveryBackoffHigh",
		"LLMSafeSpacesSafeModeActive",
		"LLMSafeSpacesHighConsecutiveFailures",
		"LLMSafeSpacesStatusUpdateConflicts",
		"LLMSafeSpacesInitContainerSlow",
		"LLMSafeSpacesAgentReloadFailures",
		"LLMSafeSpacesAgentdSlowStartup",
		"LLMSafeSpacesRelayInjectorFailures",
		"LLMSafeSpacesHighInferenceCostRate",
		"LLMSafeSpacesWorkspaceDiskUsageHigh",
		"LLMSafeSpacesLegacyAPIKeysRemaining",
	}
	for _, expectedName := range expected {
		require.True(t, alertNames[expectedName],
			"PrometheusRule must contain alert %q", expectedName)
	}

	// Old two-tier error rate alerts must be removed.
	require.False(t, alertNames["LLMSafeSpacesHighAPIErrorRate"],
		"old warning-tier LLMSafeSpacesHighAPIErrorRate must be removed (replaced by LLMSafeSpacesLowAvailability)")
	require.False(t, alertNames["LLMSafeSpacesHighAPIErrorRateCritical"],
		"old critical-tier LLMSafeSpacesHighAPIErrorRateCritical must be removed (replaced by LLMSafeSpacesLowAvailability)")
}

// TestMonitoring_DatasourceConfigMap_RendersWithLabel verifies the
// Grafana Postgres datasource ConfigMap is rendered with the correct
// sidecar label when monitoring and datasources are enabled.
func TestMonitoring_DatasourceConfigMap_RendersWithLabel(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	var cm map[string]any
	for _, d := range docs {
		if d["kind"] == "ConfigMap" && strings.Contains(metaName(d), "grafana-datasources") {
			cm = d
			break
		}
	}
	require.NotNil(t, cm, "datasource ConfigMap must render when monitoring.enabled=true")
	meta, _ := cm["metadata"].(map[string]any)
	labels, _ := meta["labels"].(map[string]any)
	require.Equal(t, "1", labels["grafana_datasource"],
		"datasource ConfigMap must have grafana_datasource=1 label for sidecar import")
	data, _ := cm["data"].(map[string]any)
	require.Contains(t, data, "llmsafespaces-postgres.yaml",
		"datasource ConfigMap must contain llmsafespaces-postgres.yaml")
}

// TestMonitoring_DatasourcesDisabled_NoConfigMap verifies the sub-toggle
// can independently disable the datasource ConfigMap.
func TestMonitoring_DatasourcesDisabled_NoConfigMap(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n  datasources:\n    enabled: false\n")
	for _, d := range docs {
		if d["kind"] == "ConfigMap" {
			require.False(t, strings.Contains(metaName(d), "grafana-datasources"),
				"datasource ConfigMap must NOT render when datasources.enabled=false")
		}
	}
}

// TestMonitoring_DashboardConfigMap_NotEmpty verifies the dashboard JSON
// files are non-trivial (not accidentally truncated or emptied).
func TestMonitoring_DashboardConfigMap_NotEmpty(t *testing.T) {
	docs := helmTemplate(t, "monitoring:\n  enabled: true\n")
	var cm map[string]any
	for _, d := range docs {
		if d["kind"] == "ConfigMap" && strings.Contains(metaName(d), "grafana-dashboards") {
			cm = d
			break
		}
	}
	require.NotNil(t, cm)
	data, _ := cm["data"].(map[string]any)
	for _, key := range []string{"operational.json", "billing.json"} {
		content, ok := data[key].(string)
		require.True(t, ok, "ConfigMap must contain key %q", key)
		require.Greater(t, len(content), 1000,
			"dashboard %q must be non-trivial (>1000 chars); got %d", key, len(content))
	}
}
