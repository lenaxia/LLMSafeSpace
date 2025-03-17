package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/errors" as apierrors
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Common errors
var (
	ErrInvalidSandbox      = errors.New("invalid sandbox: missing required fields")
	ErrInvalidExecType     = errors.New("invalid execution type: must be 'code' or 'command'")
	ErrInvalidContent      = errors.New("invalid content: cannot be empty")
	ErrExecutionFailed     = errors.New("execution failed in sandbox")
	ErrNoPackagesSpecified = errors.New("no packages specified for installation")
	ErrUnsupportedManager  = errors.New("unsupported package manager")
	ErrContextCancelled    = errors.New("execution cancelled by context")
	ErrNilCallback         = errors.New("output callback function cannot be nil")
	ErrNilMetricsService   = errors.New("metrics service cannot be nil")
)

// Service handles code and command execution
type Service struct {
	logger     *logger.Logger
	k8sClient  interfaces.KubernetesClient
	metrics    interfaces.MetricsService
}

// Ensure Service implements interfaces.ExecutionService
var _ interfaces.ExecutionService = (*Service)(nil)

// Start initializes the execution service
func (s *Service) Start() error {
	s.logger.Info("Starting execution service")
	return nil
}

// Stop cleans up the execution service
func (s *Service) Stop() error {
	s.logger.Info("Stopping execution service")
	return nil
}

// New creates a new execution service
func New(logger *logger.Logger, k8sClient interfaces.KubernetesClient, metrics interfaces.MetricsService) (*Service, error) {
	if logger == nil {
		return nil, errors.New("logger cannot be nil")
	}
	if k8sClient == nil {
		return nil, errors.New("kubernetes client cannot be nil")
	}
	if metrics == nil {
		return nil, ErrNilMetricsService
	}
	
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
		metrics:   metrics,
	}, nil
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int) (*types.ExecutionResult, error) {
	startTime := time.Now()
	userID := getUserIDFromSandbox(sandbox)
	
	// Validate inputs
	if err := s.validateExecuteParams(sandbox, execType, content); err != nil {
		s.recordExecutionError(execType, sandbox, err, userID, startTime)
		return nil, err
	}
	
	s.logger.Debug("Executing in sandbox", 
		"namespace", sandbox.Namespace,
		"name", sandbox.Name,
		"type", execType,
		"timeout", timeout,
		"content_length", len(content),
		"user_id", userID)

	// Set default timeout if not specified
	if timeout <= 0 {
		timeout = 30 // Default timeout of 30 seconds
		s.logger.Debug("Using default timeout", "timeout", timeout)
	}

	// Create execution request
	execReq := &types.ExecutionRequest{
		Type:    execType,
		Content: content,
		Timeout: timeout,
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		s.logger.Warn("Context already cancelled before execution", 
			"error", err,
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name)
		
		wrappedErr := fmt.Errorf("%w: %v", ErrContextCancelled, err)
		s.recordExecutionError(execType, sandbox, wrappedErr, userID, startTime)
		return nil, wrappedErr
	}

	// Execute code via Kubernetes API
	execResult, err := s.k8sClient.ExecuteInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq)
	if err != nil {
		s.logger.Error("Failed to execute in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"type", execType,
			"user_id", userID)
			
		// Record metrics for failed execution
		wrappedErr := fmt.Errorf("%w: %v", ErrExecutionFailed, err)
		s.recordExecutionError(execType, sandbox, wrappedErr, userID, startTime)
		return nil, wrappedErr
	}

	duration := time.Since(startTime)
	s.logger.Debug("Execution completed", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"duration_ms", duration.Milliseconds(), 
		"exit_code", execResult.ExitCode,
		"status", execResult.Status,
		"user_id", userID)
		
	// Record metrics for successful execution
	s.metrics.RecordExecution(execType, sandbox.Spec.Runtime, execResult.Status, userID, duration)

	return execResult, nil
}

// ExecuteStream executes code or a command in a sandbox and streams the output
func (s *Service) ExecuteStream(
	ctx context.Context,
	sandbox *types.Sandbox,
	execType, content string,
	timeout int,
	outputCallback func(stream, content string),
) (*types.ExecutionResult, error) {
	startTime := time.Now()
	userID := getUserIDFromSandbox(sandbox)
	
	// Validate inputs
	if err := s.validateExecuteParams(sandbox, execType, content); err != nil {
		s.recordExecutionError(execType, sandbox, err, userID, startTime)
		return nil, err
	}
	
	// Validate callback
	if outputCallback == nil {
		err := ErrNilCallback
		s.recordExecutionError(execType, sandbox, err, userID, startTime)
		return nil, err
	}
	
	s.logger.Debug("Executing stream in sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"type", execType, 
		"timeout", timeout,
		"content_length", len(content),
		"user_id", userID)

	// Set default timeout if not specified
	if timeout <= 0 {
		timeout = 30 // Default timeout of 30 seconds
		s.logger.Debug("Using default timeout for stream", "timeout", timeout)
	}

	// Create execution request
	execReq := &types.ExecutionRequest{
		Type:    execType,
		Content: content,
		Timeout: timeout,
		Stream:  true,
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		s.logger.Warn("Context already cancelled before stream execution", 
			"error", err,
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"user_id", userID)
		
		wrappedErr := fmt.Errorf("%w: %v", ErrContextCancelled, err)
		s.recordExecutionError(execType, sandbox, wrappedErr, userID, startTime)
		return nil, wrappedErr
	}

	// Execute code via Kubernetes API with streaming
	execResult, err := s.k8sClient.ExecuteStreamInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq, outputCallback)
	if err != nil {
		s.logger.Error("Failed to execute stream in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"type", execType,
			"user_id", userID)
			
		// Record metrics for failed execution
		wrappedErr := fmt.Errorf("%w: %v", ErrExecutionFailed, err)
		s.recordExecutionError(execType, sandbox, wrappedErr, userID, startTime)
		return nil, wrappedErr
	}

	duration := time.Since(startTime)
	s.logger.Debug("Stream execution completed", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"duration_ms", duration.Milliseconds(), 
		"exit_code", execResult.ExitCode,
		"status", execResult.Status,
		"user_id", userID)
		
	// Record metrics for successful execution
	s.metrics.RecordExecution(execType, sandbox.Spec.Runtime, execResult.Status, userID, duration)

	return execResult, nil
}

// InstallPackages installs packages in a sandbox
func (s *Service) InstallPackages(ctx context.Context, sandbox *types.Sandbox, packages []string, manager string) (*types.ExecutionResult, error) {
	startTime := time.Now()
	userID := getUserIDFromSandbox(sandbox)
	
	// Validate sandbox
	if sandbox == nil || sandbox.Name == "" || sandbox.Namespace == "" {
		err := ErrInvalidSandbox
		s.logger.Error("Invalid sandbox for package installation", err, "sandbox", sandbox)
		s.recordPackageInstallError(sandbox, manager, err)
		return nil, err
	}
	
	// Validate packages
	if len(packages) == 0 {
		err := ErrNoPackagesSpecified
		s.logger.Error("No packages specified for installation", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name)
		s.recordPackageInstallError(sandbox, manager, err)
		return nil, err
	}

	s.logger.Info("Installing packages in sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"packages", packages, 
		"manager", manager,
		"runtime", sandbox.Spec.Runtime,
		"user_id", userID)

	// Determine package manager command
	var cmd string
	if manager == "" {
		// Auto-detect package manager based on runtime
		runtime := sandbox.Spec.Runtime
		if runtime == "python" || runtime == "python:3.10" {
			manager = "pip"
		} else if runtime == "nodejs" || runtime == "nodejs:18" {
			manager = "npm"
		} else if runtime == "ruby" || runtime == "ruby:3.1" {
			manager = "gem"
		} else if runtime == "go" || runtime == "go:1.18" {
			manager = "go"
		} else {
			err := fmt.Errorf("unable to determine package manager for runtime: %s", runtime)
			s.logger.Error("Package manager detection failed", err, 
				"runtime", runtime,
				"namespace", sandbox.Namespace,
				"name", sandbox.Name)
			
			s.recordPackageInstallError(sandbox, "unknown", err)
			return nil, err
		}
		s.logger.Debug("Auto-detected package manager", 
			"manager", manager, 
			"runtime", sandbox.Spec.Runtime)
	}

	// Build command
	switch manager {
	case "pip":
		cmd = fmt.Sprintf("pip install %s", joinPackages(packages))
	case "npm":
		cmd = fmt.Sprintf("npm install %s", joinPackages(packages))
	case "gem":
		cmd = fmt.Sprintf("gem install %s", joinPackages(packages))
	case "go":
		cmd = fmt.Sprintf("go get %s", joinPackages(packages))
	default:
		err := fmt.Errorf("%w: %s", ErrUnsupportedManager, manager)
		s.logger.Error("Unsupported package manager", err, 
			"manager", manager,
			"namespace", sandbox.Namespace,
			"name", sandbox.Name)
		
		s.recordPackageInstallError(sandbox, manager, err)
		return nil, err
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		s.logger.Warn("Context already cancelled before package installation", 
			"error", err,
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"manager", manager)
		
		wrappedErr := fmt.Errorf("%w: %v", ErrContextCancelled, err)
		s.recordPackageInstallError(sandbox, manager, wrappedErr)
		return nil, wrappedErr
	}

	// Execute command with extended timeout for package installation
	result, err := s.Execute(ctx, sandbox, "command", cmd, 300)
	
	// Record metrics for package installation
	status := "completed"
	if err != nil || (result != nil && result.ExitCode != 0) {
		status = "failed"
	}
	s.metrics.RecordPackageInstallation(sandbox.Spec.Runtime, manager, status)
	
	return result, err
}

// validateExecuteParams validates the parameters for Execute and ExecuteStream
func (s *Service) validateExecuteParams(sandbox *types.Sandbox, execType, content string) error {
	// Validate sandbox
	if sandbox == nil {
		s.logger.Error("Nil sandbox provided", nil)
		return ErrInvalidSandbox
	}
	
	if sandbox.Name == "" {
		s.logger.Error("Sandbox name is empty", nil, "sandbox", sandbox)
		return ErrInvalidSandbox
	}
	
	if sandbox.Namespace == "" {
		s.logger.Error("Sandbox namespace is empty", nil, "sandbox", sandbox)
		return ErrInvalidSandbox
	}
	
	// Validate execution type
	if execType != "code" && execType != "command" {
		s.logger.Error("Invalid execution type", nil, 
			"type", execType,
			"namespace", sandbox.Namespace,
			"name", sandbox.Name)
		return ErrInvalidExecType
	}
	
	// Validate content
	if content == "" {
		s.logger.Error("Empty content for execution", nil, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"type", execType)
		return ErrInvalidContent
	}
	
	return nil
}

// recordExecutionError records metrics for execution errors
func (s *Service) recordExecutionError(execType string, sandbox *types.Sandbox, err error, userID string, startTime time.Time) {
	if s.metrics == nil {
		return
	}
	
	duration := time.Since(startTime)
	runtime := ""
	if sandbox != nil && sandbox.Spec.Runtime != "" {
		runtime = sandbox.Spec.Runtime
	}
	
	s.metrics.RecordExecution(execType, runtime, "failed", userID, duration)
	s.metrics.RecordError("execution", "execute", err.Error())
}

// recordPackageInstallError records metrics for package installation errors
func (s *Service) recordPackageInstallError(sandbox *types.Sandbox, manager string, err error) {
	if s.metrics == nil {
		return
	}
	
	runtime := ""
	if sandbox != nil && sandbox.Spec.Runtime != "" {
		runtime = sandbox.Spec.Runtime
	}
	
	s.metrics.RecordPackageInstallation(runtime, manager, "failed")
	s.metrics.RecordError("package_install", manager, err.Error())
}

// getUserIDFromSandbox extracts the user ID from sandbox labels
func getUserIDFromSandbox(sandbox *types.Sandbox) string {
	if sandbox == nil || sandbox.Labels == nil {
		return ""
	}
	return sandbox.Labels["user"]
}

// joinPackages joins package names with spaces
func joinPackages(packages []string) string {
	result := ""
	for i, pkg := range packages {
		if i > 0 {
			result += " "
		}
		result += pkg
	}
	return result
}
