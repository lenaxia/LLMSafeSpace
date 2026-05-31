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
//	go test ./charts/llmsafespace/...
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
		require.Len(t, ingress, 1,
			"default-deny policy %q must have exactly one allow rule (the API proxy)", name)

		// Verify the allow rule targets the API server pods on agentd ports.
		rule := ingress[0].(map[string]any)
		ports, _ := rule["ports"].([]any)
		var foundAgentdPort bool
		for _, p := range ports {
			pm := p.(map[string]any)
			if port := pm["port"]; port == float64(4097) || port == 4097 {
				foundAgentdPort = true
			}
		}
		require.True(t, foundAgentdPort,
			"default-deny policy %q must allow API proxy on agentd port 4097", name)

		from, _ := rule["from"].([]any)
		require.NotEmpty(t, from, "ingress rule must restrict the source via from selector")

		// The selector should reference the API component label.
		fromMap := from[0].(map[string]any)
		podSel, _ := fromMap["podSelector"].(map[string]any)
		matchLabels, _ := podSel["matchLabels"].(map[string]any)
		require.Equal(t, "api", matchLabels["app.kubernetes.io/component"],
			"ingress source must select the API server pods only")

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
//      pointing at /validate-llmsafespace-dev-v1-workspace.
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
			if path == "/validate-llmsafespace-dev-v1-workspace" {
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
			"ValidatingWebhookConfiguration must include a webhook for /validate-llmsafespace-dev-v1-workspace")
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
		require.NotContains(t, rv, "llmsafespace.dev/runtimeenvironments",
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
