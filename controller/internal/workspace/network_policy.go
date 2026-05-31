// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

// Per-workspace NetworkPolicy generation for F1.2.4 (Epic 17).
//
// Pre-fix: workspace.spec.networkAccess.egress was completely ignored.
// A user could declare an FQDN allow-list and expect outbound egress
// to be limited to that list; the controller never created a matching
// NetworkPolicy.
//
// Fix: when networkAccess.egress is non-empty the controller emits a
// NetworkPolicy named `workspace-egress-<workspace>` selecting just
// that workspace's pod (via LabelWorkspace) and adding egress allow
// rules for the declared FQDN list:
//   - DNS (port 53) to kube-dns (always allowed; required for any
//     DNS-resolved FQDN to work).
//   - HTTPS (443) and HTTP (80) to /32 ipBlocks resolved from each
//     declared domain at reconcile time.
//
// Trade-off: standard k8s NetworkPolicy does not natively support FQDN
// matching. We resolve domains to IPs at reconcile time (best-effort,
// 2-second timeout) and refresh on every reconcile. Operators who need
// stricter FQDN guarantees should layer a Cilium FQDN policy on top —
// out of scope for this fix.
//
// Defense-in-depth: the chart-wide G16 NetPol still applies (k8s
// NetworkPolicy is additive: union of all selecting policies' rules
// becomes the allow-list). This per-workspace policy WIDENS the
// chart-wide allow-list with the user's declared destinations.

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// dnsResolutionTimeout caps each LookupHost call so a slow / dead DNS
// resolver cannot hang the reconcile loop. Resolutions that time out
// emit no IP entries (the rule for that domain is silently skipped);
// the next reconcile will retry.
const dnsResolutionTimeout = 2 * time.Second

// HostResolver resolves a hostname to a list of IP strings. Tests
// inject a stub via WorkspaceReconciler.HostResolver to avoid hitting
// real DNS; production uses net.DefaultResolver via defaultHostResolver.
type HostResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

type defaultHostResolver struct{}

func (defaultHostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// privateOrInternalCIDRs is the deny-list of destination ranges that
// the per-workspace NetPol MUST NEVER re-allow even if a user-declared
// domain resolves into one of them. This closes the validator-found
// bypass where a `Domain: kubernetes.default.svc.cluster.local` would
// otherwise emit a /32 allow on the apiserver ClusterIP, defeating the
// chart-wide `blockedEgressCIDRs` exclusion via NetworkPolicy union
// semantics.
//
// The list mirrors the chart's `blockedEgressCIDRs` default:
//   - RFC1918 (10/8, 172.16/12, 192.168/16) — in-cluster service ranges,
//     internal admin endpoints, etc.
//   - 169.254/16 — link-local + cloud metadata.
//   - 127/8 — loopback.
//   - 224/4 — multicast.
//   - 100.64/10 — CGNAT (sometimes used for in-cluster Pod CIDRs).
//
// If an operator legitimately needs the workspace to reach an in-
// cluster address (e.g. a private LLM gateway), they should expose it
// via a public FQDN behind ingress, OR add the specific CIDR to the
// chart's `allowedEgressCIDRs` and let the chart-wide rule cover it.
var privateOrInternalCIDRs = mustParseCIDRs(
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"127.0.0.0/8",
	"224.0.0.0/4",
	"100.64.0.0/10", // CGNAT
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("invalid private CIDR constant %q: %v", c, err))
		}
		out = append(out, n)
	}
	return out
}

// isPrivateOrInternal reports whether ip falls inside any of the
// blocked ranges above.
func isPrivateOrInternal(ip net.IP) bool {
	for _, n := range privateOrInternalCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// internalDomainSuffixes is the deny-list of DNS suffixes that point
// at cluster-internal services. These never resolve outside the
// cluster and should never appear in user-declared egress allow-lists.
// Defense-in-depth alongside the IP filter — short-circuits before
// even hitting DNS.
var internalDomainSuffixes = []string{
	".cluster.local",
	".svc",
	".svc.cluster.local",
	".local",
	".internal",
}

// isInternalDomain reports whether the domain ends in a cluster-
// internal suffix. Comparison is case-insensitive.
func isInternalDomain(d string) bool {
	d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
	for _, s := range internalDomainSuffixes {
		if strings.HasSuffix(d, s) {
			return true
		}
	}
	return false
}

// workspaceEgressPolicyName returns the NetworkPolicy name for a given
// workspace. Stable so the controller can reconcile in place rather
// than generate-and-delete each pass.
func workspaceEgressPolicyName(ws *v1.Workspace) string {
	return fmt.Sprintf("workspace-egress-%s", ws.Name)
}

// buildWorkspaceEgressNetworkPolicy assembles the per-workspace
// NetworkPolicy from spec.networkAccess.egress. Returns (nil, nil)
// when no policy is needed (nil NetworkAccess or empty Egress list).
func (r *WorkspaceReconciler) buildWorkspaceEgressNetworkPolicy(
	ctx context.Context,
	ws *v1.Workspace,
) (*networkingv1.NetworkPolicy, error) {
	if ws.Spec.NetworkAccess == nil || len(ws.Spec.NetworkAccess.Egress) == 0 {
		return nil, nil
	}

	logger := log.FromContext(ctx).WithValues("workspace", ws.Name)

	// Resolver: production default unless test injection.
	resolver := r.HostResolver
	if resolver == nil {
		resolver = defaultHostResolver{}
	}

	// Collect resolved IPs across all declared domains. De-dupe so
	// the rendered NetPol is stable across reconciles when a domain
	// resolves to the same IPs.
	//
	// Filtering layers (closes the validator-found bypass class where
	// a Domain like `kubernetes.default.svc.cluster.local` would emit
	// a /32 ipBlock that re-grants in-cluster reachability via NetPol
	// union semantics):
	//  1. internal-domain suffix block — refuse `.cluster.local`,
	//     `.svc`, `.local`, `.internal` outright.
	//  2. private/internal IP block — drop any resolved IP that lands
	//     in RFC1918, 169.254/16, 127/8, 224/4, or 100.64/10. The
	//     filter is applied AFTER resolution so even an attacker-
	//     controlled public domain that DNS-rebinds to a metadata IP
	//     is dropped.
	resolvedIPs := map[string]struct{}{}
	for _, rule := range ws.Spec.NetworkAccess.Egress {
		domain := strings.TrimSpace(rule.Domain)
		if domain == "" {
			continue
		}
		if isInternalDomain(domain) {
			logger.Info("skipping internal domain in spec.networkAccess.egress",
				"domain", domain,
				"reason", "cluster-internal suffix is forbidden in user-declared egress (F1.2.4 bypass class)")
			continue
		}
		for _, ip := range resolveDomainIPv4(ctx, resolver, domain, logger) {
			parsed := net.ParseIP(ip)
			if parsed == nil {
				continue
			}
			if isPrivateOrInternal(parsed) {
				logger.Info("dropping private/internal IP from resolved domain",
					"domain", domain, "ip", ip,
					"reason", "destination is in private/internal CIDR — would defeat chart-wide blockedEgressCIDRs via NetPol union")
				continue
			}
			resolvedIPs[ip] = struct{}{}
		}
	}

	// Sort for stable rendering (helm-style determinism).
	ipList := make([]string, 0, len(resolvedIPs))
	for ip := range resolvedIPs {
		ipList = append(ipList, ip)
	}
	sort.Strings(ipList)

	port443 := intstr.FromInt(443)
	port80 := intstr.FromInt(80)
	port53 := intstr.FromInt(53)
	tcp := corev1.ProtocolTCP
	udp := corev1.ProtocolUDP

	// Always allow DNS to kube-dns so the workspace can re-resolve
	// the same domains itself (e.g. for HTTP(S) clients that don't
	// pin IPs). kube-dns lives in kube-system with label
	// k8s-app=kube-dns.
	egressRules := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"kubernetes.io/metadata.name": "kube-system",
					},
				},
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"k8s-app": "kube-dns",
					},
				},
			}},
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &udp, Port: &port53},
				{Protocol: &tcp, Port: &port53},
			},
		},
	}

	// Add HTTP(S) allow rules per resolved IP.
	if len(ipList) > 0 {
		peers := make([]networkingv1.NetworkPolicyPeer, 0, len(ipList))
		for _, ip := range ipList {
			peers = append(peers, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: ip + "/32"},
			})
		}
		egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
			To: peers,
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &tcp, Port: &port443},
				{Protocol: &tcp, Port: &port80},
			},
		})
	}

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workspaceEgressPolicyName(ws),
			Namespace: ws.Namespace,
			Labels: map[string]string{
				LabelWorkspace:                 ws.Name,
				"app.kubernetes.io/component":  "workspace-network-policy",
				"app.kubernetes.io/managed-by": "llmsafespace-controller",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{LabelWorkspace: ws.Name},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egressRules,
		},
	}
	return np, nil
}

// resolveDomainIPv4 looks up a domain via the given resolver and
// returns the IPv4 addresses as dotted-quad strings. On timeout or
// NXDOMAIN it logs and returns an empty slice (the next reconcile
// will retry; a transient DNS failure should not break a running
// workspace's egress).
func resolveDomainIPv4(ctx context.Context, resolver HostResolver, domain string, logger logr.Logger) []string {
	c, cancel := context.WithTimeout(ctx, dnsResolutionTimeout)
	defer cancel()

	addrs, err := resolver.LookupHost(c, domain)
	if err != nil {
		logger.V(1).Info("DNS lookup failed (will retry next reconcile)",
			"domain", domain, "error", err.Error())
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		// IPv4 only — k8s NetworkPolicy ipBlock semantics differ for
		// v6 and we conservatively scope this to v4 first.
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}

// ensureWorkspaceEgressNetworkPolicy creates or updates the per-workspace
// egress NetworkPolicy. No-op when no NetPol is needed (nil
// NetworkAccess or empty Egress); deletes any existing per-workspace
// policy in that case so toggling NetworkAccess off cleanly removes
// the rule.
func (r *WorkspaceReconciler) ensureWorkspaceEgressNetworkPolicy(
	ctx context.Context,
	ws *v1.Workspace,
) error {
	desired, err := r.buildWorkspaceEgressNetworkPolicy(ctx, ws)
	if err != nil {
		return err
	}

	name := workspaceEgressPolicyName(ws)
	existing := &networkingv1.NetworkPolicy{}
	getErr := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: name}, existing)

	if desired == nil {
		// User toggled NetworkAccess off — delete any leftover policy.
		if getErr == nil {
			if delErr := r.Delete(ctx, existing); delErr != nil && !apierrors.IsNotFound(delErr) {
				return fmt.Errorf("deleting per-workspace egress NetPol: %w", delErr)
			}
		}
		return nil
	}

	// Set owner ref so the NetPol is GC'd when the Workspace is.
	if err := controllerutil.SetControllerReference(ws, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner ref on egress NetPol: %w", err)
	}

	if apierrors.IsNotFound(getErr) {
		if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating per-workspace egress NetPol: %w", err)
		}
		return nil
	}
	if getErr != nil {
		return fmt.Errorf("getting per-workspace egress NetPol: %w", getErr)
	}

	// Update spec in place. Compare-then-update keeps the controller
	// idempotent when DNS resolves the same IPs across reconciles.
	if reflect.DeepEqual(existing.Spec, desired.Spec) &&
		reflect.DeepEqual(existing.Labels, desired.Labels) {
		return nil
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("updating per-workspace egress NetPol: %w", err)
	}
	return nil
}
