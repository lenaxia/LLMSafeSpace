package warmpool

import (
	"context"
	"fmt"
	"strings"

	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Service handles warm pool operations
type Service struct {
	logger     *logger.Logger
	k8sClient  *kubernetes.Client
	dbService  *database.Service
	metricsSvc *metrics.Service
}

// New creates a new warm pool service
func New(
	logger *logger.Logger,
	k8sClient *kubernetes.Client,
	dbService *database.Service,
	metricsSvc *metrics.Service,
) (*Service, error) {
	return &Service{
		logger:     logger,
		k8sClient:  k8sClient,
		dbService:  dbService,
		metricsSvc: metricsSvc,
	}, nil
}

// CheckAvailability checks if warm pods are available for a given runtime and security level
func (s *Service) CheckAvailability(ctx context.Context, runtime, securityLevel string) (bool, error) {
	// Create selector for warm pools
	selector := labels.SelectorFromSet(labels.Set{
		"runtime":        strings.Replace(runtime, ":", "-", -1),
		"security-level": securityLevel,
	})

	// List warm pools
	warmPools, err := s.k8sClient.LlmsafespaceV1().WarmPools("").List(metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return false, fmt.Errorf("failed to list warm pools: %w", err)
	}

	// Check if any pool has available pods
	for _, pool := range warmPools.Items {
		if pool.Status.AvailablePods > 0 {
			return true, nil
		}
	}

	return false, nil
}

// CreateWarmPool creates a new warm pool
func (s *Service) CreateWarmPool(ctx context.Context, req CreateWarmPoolRequest) (*llmsafespacev1.WarmPool, error) {
	// Set default namespace if not provided
	if req.Namespace == "" {
		req.Namespace = "default"
	}

	// Set default security level if not provided
	if req.SecurityLevel == "" {
		req.SecurityLevel = "standard"
	}

	// Set default min size if not provided
	if req.MinSize <= 0 {
		req.MinSize = 1
	}

	// Create warm pool object
	warmPool := &llmsafespacev1.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels: map[string]string{
				"app":            "llmsafespace",
				"user-id":        req.UserID,
				"runtime":        strings.Replace(req.Runtime, ":", "-", -1),
				"security-level": req.SecurityLevel,
			},
		},
		Spec: llmsafespacev1.WarmPoolSpec{
			Runtime:       req.Runtime,
			MinSize:       req.MinSize,
			MaxSize:       req.MaxSize,
			SecurityLevel: req.SecurityLevel,
			TTL:           req.TTL,
			Resources:     req.Resources,
			ProfileRef:    req.ProfileRef,
			PreloadPackages: req.PreloadPackages,
			PreloadScripts:  req.PreloadScripts,
			AutoScaling:     req.AutoScaling,
		},
	}

	// Create the warm pool in Kubernetes
	result, err := s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Create(warmPool)
	if err != nil {
		return nil, fmt.Errorf("failed to create warm pool: %w", err)
	}

	// Store warm pool metadata in database
	err = s.storeWarmPoolMetadata(ctx, result.Name, req.Namespace, req.UserID, req.Runtime)
	if err != nil {
		// Attempt to clean up the Kubernetes resource
		_ = s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Delete(result.Name, metav1.DeleteOptions{})
		return nil, fmt.Errorf("failed to store warm pool metadata: %w", err)
	}

	return result, nil
}

// GetWarmPool gets a warm pool by name
func (s *Service) GetWarmPool(ctx context.Context, name, namespace string) (*llmsafespacev1.WarmPool, error) {
	// Set default namespace if not provided
	if namespace == "" {
		namespace = "default"
	}

	// Get warm pool from Kubernetes
	warmPool, err := s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get warm pool: %w", err)
	}

	return warmPool, nil
}

// ListWarmPools lists warm pools for a user
func (s *Service) ListWarmPools(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	// Get warm pools from database
	warmPools, err := s.listWarmPoolsFromDB(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list warm pools: %w", err)
	}

	// Enrich with Kubernetes data
	for i, warmPool := range warmPools {
		name := warmPool["name"].(string)
		namespace := warmPool["namespace"].(string)
		k8sWarmPool, err := s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Get(name, metav1.GetOptions{})
		if err == nil {
			warmPools[i]["available_pods"] = k8sWarmPool.Status.AvailablePods
			warmPools[i]["assigned_pods"] = k8sWarmPool.Status.AssignedPods
			warmPools[i]["pending_pods"] = k8sWarmPool.Status.PendingPods
		}
	}

	return warmPools, nil
}

// UpdateWarmPool updates a warm pool
func (s *Service) UpdateWarmPool(ctx context.Context, req UpdateWarmPoolRequest) (*llmsafespacev1.WarmPool, error) {
	// Set default namespace if not provided
	if req.Namespace == "" {
		req.Namespace = "default"
	}

	// Get warm pool from Kubernetes
	warmPool, err := s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Get(req.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get warm pool: %w", err)
	}

	// Update fields
	if req.MinSize > 0 {
		warmPool.Spec.MinSize = req.MinSize
	}
	if req.MaxSize > 0 {
		warmPool.Spec.MaxSize = req.MaxSize
	}
	if req.TTL > 0 {
		warmPool.Spec.TTL = req.TTL
	}
	if req.AutoScaling != nil {
		warmPool.Spec.AutoScaling = req.AutoScaling
	}

	// Update the warm pool in Kubernetes
	result, err := s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Update(warmPool)
	if err != nil {
		return nil, fmt.Errorf("failed to update warm pool: %w", err)
	}

	return result, nil
}

// DeleteWarmPool deletes a warm pool
func (s *Service) DeleteWarmPool(ctx context.Context, name, namespace string) error {
	// Set default namespace if not provided
	if namespace == "" {
		namespace = "default"
	}

	// Get warm pool metadata from database
	metadata, err := s.getWarmPoolMetadata(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get warm pool metadata: %w", err)
	}

	if metadata == nil {
		return fmt.Errorf("warm pool not found: %s", name)
	}

	// Delete warm pool from Kubernetes
	err = s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Delete(name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete warm pool: %w", err)
	}

	// Delete warm pool metadata from database
	err = s.deleteWarmPoolMetadata(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to delete warm pool metadata: %w", err)
	}

	return nil
}

// Helper methods for database operations

// storeWarmPoolMetadata stores warm pool metadata in the database
func (s *Service) storeWarmPoolMetadata(ctx context.Context, name, namespace, userID, runtime string) error {
	// In a real implementation, this would store the data in the database
	// For now, we'll just log it
	s.logger.Info("Storing warm pool metadata", 
		"name", name,
		"namespace", namespace,
		"userID", userID,
		"runtime", runtime)
	return nil
}

// getWarmPoolMetadata gets warm pool metadata from the database
func (s *Service) getWarmPoolMetadata(ctx context.Context, name string) (map[string]interface{}, error) {
	// In a real implementation, this would retrieve data from the database
	// For now, we'll return a mock response
	return map[string]interface{}{
		"name":      name,
		"user_id":   "mock-user",
		"runtime":   "python:3.10",
		"namespace": "default",
	}, nil
}

// listWarmPoolsFromDB lists warm pools for a user from the database
func (s *Service) listWarmPoolsFromDB(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	// In a real implementation, this would query the database
	// For now, we'll return mock data
	return []map[string]interface{}{
		{
			"name":      "warm-pool-1",
			"namespace": "default",
			"runtime":   "python:3.10",
		},
		{
			"name":      "warm-pool-2",
			"namespace": "default",
			"runtime":   "nodejs:16",
		},
	}, nil
}

// deleteWarmPoolMetadata deletes warm pool metadata from the database
func (s *Service) deleteWarmPoolMetadata(ctx context.Context, name string) error {
	// In a real implementation, this would delete data from the database
	// For now, we'll just log it
	s.logger.Info("Deleting warm pool metadata", "name", name)
	return nil
}

// GetWarmSandbox gets a warm sandbox for a given runtime
func (s *Service) GetWarmSandbox(ctx context.Context, runtime string) (string, error) {
	// List warm pods with the given runtime
	selector := labels.SelectorFromSet(labels.Set{
		"runtime": strings.Replace(runtime, ":", "-", -1),
		"status":  "available",
	})

	// List warm pods
	warmPods, err := s.k8sClient.LlmsafespaceV1().WarmPods("default").List(metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list warm pods: %w", err)
	}

	// Check if any pods are available
	if len(warmPods.Items) == 0 {
		return "", fmt.Errorf("no warm pods available for runtime: %s", runtime)
	}

	// Get the first available pod
	pod := warmPods.Items[0]

	// Record warm pool hit in metrics
	s.metricsSvc.RecordWarmPoolHit()

	return pod.Name, nil
}

// RemoveFromWarmPool removes a sandbox from the warm pool
func (s *Service) RemoveFromWarmPool(ctx context.Context, sandboxID string) error {
	// Delete the warm pod
	err := s.k8sClient.LlmsafespaceV1().WarmPods("default").Delete(sandboxID, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from warm pool: %w", err)
	}

	return nil
}

// AddToWarmPool adds a sandbox to the warm pool
func (s *Service) AddToWarmPool(ctx context.Context, sandboxID, runtime string) error {
	// Create a new warm pod
	warmPod := &llmsafespacev1.WarmPod{
		ObjectMeta: metav1.ObjectMeta{
			Name: sandboxID,
			Labels: map[string]string{
				"runtime": strings.Replace(runtime, ":", "-", -1),
				"status":  "available",
			},
		},
		Spec: llmsafespacev1.WarmPodSpec{
			Runtime: runtime,
		},
	}

	// Create the warm pod
	_, err := s.k8sClient.LlmsafespaceV1().WarmPods("default").Create(warmPod)
	if err != nil {
		return fmt.Errorf("failed to add sandbox to warm pool: %w", err)
	}

	return nil
}

// GetWarmPoolStatus gets the status of a warm pool
func (s *Service) GetWarmPoolStatus(ctx context.Context, name, namespace string) (*llmsafespacev1.WarmPoolStatus, error) {
	// Set default namespace if not provided
	if namespace == "" {
		namespace = "default"
	}

	// Get warm pool from Kubernetes
	warmPool, err := s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get warm pool: %w", err)
	}

	return &warmPool.Status, nil
}

// GetWarmPoolStatus gets the status of all warm pools
func (s *Service) GetWarmPoolStatus(ctx context.Context) (map[string]interface{}, error) {
	// List all warm pools
	warmPools, err := s.k8sClient.LlmsafespaceV1().WarmPools("").List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list warm pools: %w", err)
	}

	// Collect status information
	status := make(map[string]interface{})
	runtimes := make(map[string]map[string]interface{})

	for _, pool := range warmPools.Items {
		runtime := pool.Spec.Runtime
		if _, exists := runtimes[runtime]; !exists {
			runtimes[runtime] = map[string]interface{}{
				"available": 0,
				"pending":   0,
				"assigned":  0,
				"total":     0,
			}
		}

		runtimeStats := runtimes[runtime]
		runtimeStats["available"] = runtimeStats["available"].(int) + pool.Status.AvailablePods
		runtimeStats["pending"] = runtimeStats["pending"].(int) + pool.Status.PendingPods
		runtimeStats["assigned"] = runtimeStats["assigned"].(int) + pool.Status.AssignedPods
		runtimeStats["total"] = runtimeStats["total"].(int) + pool.Status.TotalPods
	}

	status["runtimes"] = runtimes
	status["total_pools"] = len(warmPools.Items)

	return status, nil
}

// CreateWarmPoolRequest defines the request for creating a warm pool
type CreateWarmPoolRequest struct {
	Name            string                                `json:"name"`
	Runtime         string                                `json:"runtime"`
	MinSize         int                                   `json:"minSize"`
	MaxSize         int                                   `json:"maxSize,omitempty"`
	SecurityLevel   string                                `json:"securityLevel,omitempty"`
	TTL             int                                   `json:"ttl,omitempty"`
	Resources       *llmsafespacev1.ResourceRequirements  `json:"resources,omitempty"`
	ProfileRef      *llmsafespacev1.ProfileReference      `json:"profileRef,omitempty"`
	PreloadPackages []string                              `json:"preloadPackages,omitempty"`
	PreloadScripts  []llmsafespacev1.PreloadScript        `json:"preloadScripts,omitempty"`
	AutoScaling     *llmsafespacev1.AutoScalingConfig     `json:"autoScaling,omitempty"`
	UserID          string                                `json:"-"`
	Namespace       string                                `json:"-"`
}

// UpdateWarmPoolRequest defines the request for updating a warm pool
type UpdateWarmPoolRequest struct {
	Name        string                            `json:"name"`
	MinSize     int                               `json:"minSize,omitempty"`
	MaxSize     int                               `json:"maxSize,omitempty"`
	TTL         int                               `json:"ttl,omitempty"`
	AutoScaling *llmsafespacev1.AutoScalingConfig `json:"autoScaling,omitempty"`
	UserID      string                            `json:"-"`
	Namespace   string                            `json:"-"`
}
