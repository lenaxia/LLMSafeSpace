package warmpool

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

const (
	defaultCacheTTL = 10 * time.Second
)

type service struct {
	logger      pkginterfaces.LoggerInterface
	k8sClient   pkginterfaces.KubernetesClient
	cacheClient interfaces.CacheService
	dbClient    interfaces.DatabaseService
}

// NewService creates a new warm pool service
func NewService(
	logger pkginterfaces.LoggerInterface,
	k8sClient pkginterfaces.KubernetesClient,
	cacheClient interfaces.CacheService,
	dbClient interfaces.DatabaseService,
) interfaces.WarmPoolService {
	return &service{
		logger:      logger.With("component", "warmpool-service"),
		k8sClient:   k8sClient,
		cacheClient: cacheClient,
		dbClient:    dbClient,
	}
}

// CheckAvailability checks if warm pods are available for a given runtime and security level
func (s *service) CheckAvailability(ctx context.Context, runtime, securityLevel string) (bool, error) {
	// Check cache first for faster response
	cacheKey := fmt.Sprintf("warmpool:availability:%s:%s", runtime, securityLevel)
	if availableStr, err := s.cacheClient.Get(ctx, cacheKey); err == nil && availableStr != "" {
		return availableStr == "true", nil
	}

	// Normalize runtime for label selector
	normalizedRuntime := strings.Replace(runtime, ":", "-", -1)
	
	// Create label selector
	selector := labels.SelectorFromSet(labels.Set{
		"runtime":        normalizedRuntime,
		"securityLevel":  securityLevel,
	})

	// List warm pools matching the criteria
	warmPools, err := s.k8sClient.LlmsafespaceV1().WarmPools("").List(metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return false, fmt.Errorf("failed to list warm pools: %w", err)
	}

	// Check if any pool has available pods
	available := false
	for _, pool := range warmPools.Items {
		if pool.Status.AvailablePods > 0 {
			available = true
			break
		}
	}

	// Cache the result for a short period
	err = s.cacheClient.Set(ctx, cacheKey, fmt.Sprintf("%t", available), defaultCacheTTL)
	if err != nil {
		s.logger.Error("Failed to cache warm pool availability", err,
			"runtime", runtime,
			"securityLevel", securityLevel)
		// Continue even if caching fails
	}

	return available, nil
}

// GetWarmSandbox gets a warm sandbox for the given runtime
func (s *service) GetWarmSandbox(ctx context.Context, runtime string) (string, error) {
	// This would typically involve finding an available warm pod and returning its ID
	// For now, we'll return an error as this needs to be implemented by the controller
	return "", fmt.Errorf("warm pod allocation must be handled by the controller")
}

// AddToWarmPool adds a sandbox to the warm pool
func (s *service) AddToWarmPool(ctx context.Context, sandboxID, runtime string) error {
	// This would typically involve marking a sandbox as available in the warm pool
	// For now, we'll return an error as this needs to be implemented by the controller
	return fmt.Errorf("adding to warm pool must be handled by the controller")
}

// RemoveFromWarmPool removes a sandbox from the warm pool
func (s *service) RemoveFromWarmPool(ctx context.Context, sandboxID string) error {
	// This would typically involve marking a sandbox as no longer available in the warm pool
	// For now, we'll return an error as this needs to be implemented by the controller
	return fmt.Errorf("removing from warm pool must be handled by the controller")
}

// GetWarmPoolStatus gets the status of a warm pool
func (s *service) GetWarmPoolStatus(ctx context.Context, name, namespace string) (map[string]interface{}, error) {
	// Get warm pool
	warmPool, err := s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("warm pool %s not found in namespace %s", name, namespace)
		}
		return nil, fmt.Errorf("failed to get warm pool: %w", err)
	}

	// Convert to map
	status := map[string]interface{}{
		"name":          warmPool.Name,
		"namespace":     warmPool.Namespace,
		"runtime":       warmPool.Spec.Runtime,
		"minSize":       warmPool.Spec.MinSize,
		"maxSize":       warmPool.Spec.MaxSize,
		"availablePods": warmPool.Status.AvailablePods,
		"assignedPods":  warmPool.Status.AssignedPods,
		"pendingPods":   warmPool.Status.PendingPods,
	}

	if warmPool.Status.LastScaleTime != nil {
		status["lastScaleTime"] = warmPool.Status.LastScaleTime.Time
	}

	// Add conditions
	conditions := make([]map[string]interface{}, 0, len(warmPool.Status.Conditions))
	for _, condition := range warmPool.Status.Conditions {
		conditions = append(conditions, map[string]interface{}{
			"type":               condition.Type,
			"status":             condition.Status,
			"reason":             condition.Reason,
			"message":            condition.Message,
			"lastTransitionTime": condition.LastTransitionTime.Time,
		})
	}
	status["conditions"] = conditions

	return status, nil
}

// GetGlobalWarmPoolStatus gets the global status of all warm pools
func (s *service) GetGlobalWarmPoolStatus(ctx context.Context) (map[string]interface{}, error) {
	// List all warm pools
	warmPools, err := s.k8sClient.LlmsafespaceV1().WarmPools("").List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list warm pools: %w", err)
	}

	// Aggregate stats
	totalAvailable := 0
	totalAssigned := 0
	totalPending := 0
	runtimeStats := make(map[string]map[string]int)

	for _, pool := range warmPools.Items {
		totalAvailable += pool.Status.AvailablePods
		totalAssigned += pool.Status.AssignedPods
		totalPending += pool.Status.PendingPods

		// Track stats by runtime
		runtime := pool.Spec.Runtime
		if _, exists := runtimeStats[runtime]; !exists {
			runtimeStats[runtime] = map[string]int{
				"available": 0,
				"assigned":  0,
				"pending":   0,
				"total":     0,
			}
		}

		runtimeStats[runtime]["available"] += pool.Status.AvailablePods
		runtimeStats[runtime]["assigned"] += pool.Status.AssignedPods
		runtimeStats[runtime]["pending"] += pool.Status.PendingPods
		runtimeStats[runtime]["total"] += pool.Status.AvailablePods + pool.Status.AssignedPods + pool.Status.PendingPods
	}

	// Build response
	status := map[string]interface{}{
		"totalPools":     len(warmPools.Items),
		"totalAvailable": totalAvailable,
		"totalAssigned":  totalAssigned,
		"totalPending":   totalPending,
		"runtimeStats":   runtimeStats,
	}

	return status, nil
}

// CreateWarmPool creates a new warm pool
func (s *service) CreateWarmPool(ctx context.Context, req types.CreateWarmPoolRequest) (*types.WarmPool, error) {
	// Validate request
	if err := validateCreateWarmPoolRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Create warm pool object
	warmPool := &types.WarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels: map[string]string{
				"app":           "llmsafespace",
				"runtime":       strings.Replace(req.Runtime, ":", "-", -1),
				"securityLevel": req.SecurityLevel,
				"user-id":       req.UserID,
			},
		},
		Spec: types.WarmPoolSpec{
			Runtime:         req.Runtime,
			MinSize:         req.MinSize,
			MaxSize:         req.MaxSize,
			SecurityLevel:   req.SecurityLevel,
			TTL:             req.TTL,
			Resources:       req.Resources,
			ProfileRef:      req.ProfileRef,
			PreloadPackages: req.PreloadPackages,
			PreloadScripts:  req.PreloadScripts,
			AutoScaling:     req.AutoScaling,
		},
	}

	// Create warm pool in Kubernetes
	created, err := s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Create(warmPool)
	if err != nil {
		return nil, fmt.Errorf("failed to create warm pool: %w", err)
	}

	return created, nil
}

// GetWarmPool gets a warm pool by name
func (s *service) GetWarmPool(ctx context.Context, name, namespace string) (*types.WarmPool, error) {
	return s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Get(name, metav1.GetOptions{})
}

// ListWarmPools lists warm pools for a user
func (s *service) ListWarmPools(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	// List warm pools with user ID label
	selector := labels.SelectorFromSet(labels.Set{
		"user-id": userID,
	})

	warmPools, err := s.k8sClient.LlmsafespaceV1().WarmPools("").List(metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list warm pools: %w", err)
	}

	// Apply limit and offset
	start := offset
	if start >= len(warmPools.Items) {
		start = len(warmPools.Items)
	}

	end := start + limit
	if end > len(warmPools.Items) {
		end = len(warmPools.Items)
	}

	// Convert to map
	result := make([]map[string]interface{}, 0, end-start)
	for i := start; i < end; i++ {
		pool := warmPools.Items[i]
		result = append(result, map[string]interface{}{
			"name":          pool.Name,
			"namespace":     pool.Namespace,
			"runtime":       pool.Spec.Runtime,
			"minSize":       pool.Spec.MinSize,
			"maxSize":       pool.Spec.MaxSize,
			"securityLevel": pool.Spec.SecurityLevel,
			"availablePods": pool.Status.AvailablePods,
			"assignedPods":  pool.Status.AssignedPods,
			"pendingPods":   pool.Status.PendingPods,
			"createdAt":     pool.CreationTimestamp.Time,
		})
	}

	return result, nil
}

// UpdateWarmPool updates a warm pool
func (s *service) UpdateWarmPool(ctx context.Context, req types.UpdateWarmPoolRequest) (*types.WarmPool, error) {
	// Validate request
	if err := validateUpdateWarmPoolRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Get existing warm pool
	warmPool, err := s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Get(req.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("warm pool %s not found in namespace %s", req.Name, req.Namespace)
		}
		return nil, fmt.Errorf("failed to get warm pool: %w", err)
	}

	// Check ownership
	isOwner, err := s.dbClient.CheckResourceOwnership(req.UserID, "warmpool", warmPool.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to check resource ownership: %w", err)
	}
	if !isOwner {
		return nil, fmt.Errorf("user %s does not own warm pool %s", req.UserID, warmPool.Name)
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

	// Update warm pool in Kubernetes
	updated, err := s.k8sClient.LlmsafespaceV1().WarmPools(req.Namespace).Update(warmPool)
	if err != nil {
		return nil, fmt.Errorf("failed to update warm pool: %w", err)
	}

	return updated, nil
}

// DeleteWarmPool deletes a warm pool
func (s *service) DeleteWarmPool(ctx context.Context, name, namespace string) error {
	return s.k8sClient.LlmsafespaceV1().WarmPools(namespace).Delete(name, metav1.DeleteOptions{})
}

// Start initializes the warm pool service
func (s *service) Start() error {
	s.logger.Info("Starting warm pool service")
	return nil
}

// Stop cleans up the warm pool service
func (s *service) Stop() error {
	s.logger.Info("Stopping warm pool service")
	return nil
}

// Helper functions

func validateCreateWarmPoolRequest(req types.CreateWarmPoolRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}
	if req.MinSize < 0 {
		return fmt.Errorf("minSize must be non-negative")
	}
	if req.MaxSize < 0 {
		return fmt.Errorf("maxSize must be non-negative")
	}
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		return fmt.Errorf("minSize cannot be greater than maxSize")
	}
	return nil
}

func validateUpdateWarmPoolRequest(req types.UpdateWarmPoolRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.MinSize < 0 {
		return fmt.Errorf("minSize must be non-negative")
	}
	if req.MaxSize < 0 {
		return fmt.Errorf("maxSize must be non-negative")
	}
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		return fmt.Errorf("minSize cannot be greater than maxSize")
	}
	return nil
}
