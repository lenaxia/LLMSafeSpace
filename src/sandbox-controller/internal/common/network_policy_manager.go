package common

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
)

// NetworkPolicyManager handles network policy creation and management
type NetworkPolicyManager struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// NewNetworkPolicyManager creates a new NetworkPolicyManager
func NewNetworkPolicyManager(client client.Client, scheme *runtime.Scheme) *NetworkPolicyManager {
	return &NetworkPolicyManager{
		Client: client,
		Scheme: scheme,
	}
}

// CreateDefaultDenyPolicy creates a default deny policy for a sandbox
func (n *NetworkPolicyManager) CreateDefaultDenyPolicy(ctx context.Context, sandbox *resources.Sandbox) error {
	// Define the network policy
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-default-deny", sandbox.Name),
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				LabelApp:       "llmsafespace",
				LabelComponent: ComponentSandbox,
				LabelSandboxID: sandbox.Name,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelSandboxID: sandbox.Name,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}
	
	// Set the owner reference
	if err := controllerutil.SetControllerReference(sandbox, policy, n.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference on network policy: %w", err)
	}
	
	// Create the network policy
	if err := n.Client.Create(ctx, policy); err != nil {
		return fmt.Errorf("failed to create default deny network policy: %w", err)
	}
	
	return nil
}

// CreateEgressPolicies creates egress policies for a sandbox
func (n *NetworkPolicyManager) CreateEgressPolicies(ctx context.Context, sandbox *resources.Sandbox) error {
	if sandbox.Spec.NetworkAccess == nil || len(sandbox.Spec.NetworkAccess.Egress) == 0 {
		return nil
	}
	
	// Create a network policy for each egress rule
	for i := range sandbox.Spec.NetworkAccess.Egress {
		// Define the network policy
		policy := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-egress-%d", sandbox.Name, i),
				Namespace: sandbox.Namespace,
				Labels: map[string]string{
					LabelApp:       "llmsafespace",
					LabelComponent: ComponentSandbox,
					LabelSandboxID: sandbox.Name,
				},
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						LabelSandboxID: sandbox.Name,
					},
				},
				PolicyTypes: []networkingv1.PolicyType{
					networkingv1.PolicyTypeEgress,
				},
				Egress: []networkingv1.NetworkPolicyEgressRule{
					{
						// Configure egress rule based on the domain and ports
						To: []networkingv1.NetworkPolicyPeer{
							{
								IPBlock: &networkingv1.IPBlock{
									CIDR: "0.0.0.0/0",
								},
							},
						},
						Ports: []networkingv1.NetworkPolicyPort{
							{
								Port: &intstr.IntOrString{Type: intstr.Int, IntVal: 80},
							},
							{
								Port: &intstr.IntOrString{Type: intstr.Int, IntVal: 443},
							},
						},
					},
				},
			},
		}
		
		// Set the owner reference
		if err := controllerutil.SetControllerReference(sandbox, policy, n.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on network policy: %w", err)
		}
		
		// Create the network policy
		if err := n.Client.Create(ctx, policy); err != nil {
			return fmt.Errorf("failed to create egress network policy: %w", err)
		}
	}
	
	return nil
}

// DeleteNetworkPolicies deletes all network policies for a sandbox
func (n *NetworkPolicyManager) DeleteNetworkPolicies(ctx context.Context, sandbox *resources.Sandbox) error {
	// List all network policies for this sandbox
	policyList := &networkingv1.NetworkPolicyList{}
	if err := n.Client.List(ctx, policyList, client.InNamespace(sandbox.Namespace), client.MatchingLabels{
		LabelSandboxID: sandbox.Name,
	}); err != nil {
		return fmt.Errorf("failed to list network policies: %w", err)
	}
	
	// Delete each network policy
	for _, policy := range policyList.Items {
		if err := n.Client.Delete(ctx, &policy); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete network policy %s: %w", policy.Name, err)
			}
		}
	}
	
	return nil
}
