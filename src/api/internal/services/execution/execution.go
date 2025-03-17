package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Common errors
var (
	ErrInvalidSandbox     = errors.New("invalid sandbox: missing required fields")
	ErrInvalidExecType    = errors.New("invalid execution type: must be 'code' or 'command'")
	ErrInvalidContent     = errors.New("invalid content: cannot be empty")
	ErrExecutionFailed    = errors.New("execution failed in sandbox")
	ErrNoPackagesSpecified = errors.New("no packages specified for installation")
	ErrUnsupportedManager = errors.New("unsupported package manager")
	ErrContextCancelled   = errors.New("execution cancelled by context")
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
	
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
		metrics:   metrics,
	}, nil
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int) (*types.ExecutionResult, error) {
	// Validate inputs
	if err := s.validateExecuteParams(sandbox, execType, content); err != nil {
		return nil, err
	}
	
	s.logger.Debug("Executing in sandbox", 
		"namespace", sandbox.Namespace,
		"name", sandbox.Name,
		"type", execType,
		"timeout", timeout,
		"content_length", len(content))

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
		return nil, fmt.Errorf("%w: %v", ErrContextCancelled, err)
	}

	startTime := time.Now()
	// Execute code via Kubernetes API
	execResult, err := s.k8sClient.ExecuteInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq)
	if err != nil {
		s.logger.Error("Failed to execute in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"type", execType)
			
		// Record metrics for failed execution
		if s.metrics != nil {
			duration := time.Since(startTime)
			s.metrics.RecordExecution(execType, sandbox.Spec.Runtime, "failed", 
				sandbox.Labels["user"], duration)
			s.metrics.RecordError("execution", "execute", err.Error())
		}
		
		return nil, fmt.Errorf("%w: %v", ErrExecutionFailed, err)
	}

	duration := time.Since(startTime)
	s.logger.Debug("Execution completed", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"duration_ms", duration.Milliseconds(), 
		"exit_code", execResult.ExitCode,
		"status", execResult.Status)
		
	// Record metrics for successful execution
	if s.metrics != nil {
		s.metrics.RecordExecution(execType, sandbox.Spec.Runtime, execResult.Status, 
			sandbox.Labels["user"], duration)
	}

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
	// Validate inputs
	if err := s.validateExecuteParams(sandbox, execType, content); err != nil {
		return nil, err
	}
	
	// Validate callback
	if outputCallback == nil {
		return nil, errors.New("output callback function cannot be nil")
	}
	
	s.logger.Debug("Executing stream in sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"type", execType, 
		"timeout", timeout,
		"content_length", len(content))

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
			"name", sandbox.Name)
		return nil, fmt.Errorf("%w: %v", ErrContextCancelled, err)
	}

	startTime := time.Now()
	// Execute code via Kubernetes API with streaming
	execResult, err := s.k8sClient.ExecuteStreamInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq, outputCallback)
	if err != nil {
		s.logger.Error("Failed to execute stream in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name,
			"type", execType)
			
		// Record metrics for failed execution
		if s.metrics != nil {
			duration := time.Since(startTime)
			s.metrics.RecordExecution(execType, sandbox.Spec.Runtime, "failed", 
				sandbox.Labels["user"], duration)
			s.metrics.RecordError("execution_stream", "execute_stream", err.Error())
		}
		
		return nil, fmt.Errorf("%w: %v", ErrExecutionFailed, err)
	}

	duration := time.Since(startTime)
	s.logger.Debug("Stream execution completed", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"duration_ms", duration.Milliseconds(), 
		"exit_code", execResult.ExitCode,
		"status", execResult.Status)
		
	// Record metrics for successful execution
	if s.metrics != nil {
		s.metrics.RecordExecution(execType, sandbox.Spec.Runtime, execResult.Status, 
			sandbox.Labels["user"], duration)
	}

	return execResult, nil
}

// InstallPackages installs packages in a sandbox
func (s *Service) InstallPackages(ctx context.Context, sandbox *types.Sandbox, packages []string, manager string) (*types.ExecutionResult, error) {
	startTime := time.Now()
	
	// Validate sandbox
	if sandbox == nil || sandbox.Name == "" || sandbox.Namespace == "" {
		return nil, ErrInvalidSandbox
	}
	
	// Validate packages
	if len(packages) == 0 {
		return nil, ErrNoPackagesSpecified
	}

	s.logger.Info("Installing packages in sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"packages", packages, 
		"manager", manager,
		"runtime", sandbox.Spec.Runtime)

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
				"runtime", runtime)
			
			if s.metrics != nil {
				s.metrics.RecordPackageInstallation(runtime, "unknown", "failed")
				s.metrics.RecordError("package_install", "auto_detect_manager", err.Error())
			}
			
			return nil, err
		}
		s.logger.Debug("Auto-detected package manager", "manager", manager, "runtime", sandbox.Spec.Runtime)
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
		s.logger.Error("Unsupported package manager", err, "manager", manager)
		
		if s.metrics != nil {
			s.metrics.RecordPackageInstallation(sandbox.Spec.Runtime, manager, "failed")
			s.metrics.RecordError("package_install", "unsupported_manager", manager)
		}
		
		return nil, err
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		s.logger.Warn("Context already cancelled before package installation", 
			"error", err,
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name)
		return nil, fmt.Errorf("%w: %v", ErrContextCancelled, err)
	}

	// Execute command with extended timeout for package installation
	result, err := s.Execute(ctx, sandbox, "command", cmd, 300)
	
	// Record metrics for package installation
	if s.metrics != nil {
		status := "completed"
		if err != nil || (result != nil && result.ExitCode != 0) {
			status = "failed"
		}
		s.metrics.RecordPackageInstallation(sandbox.Spec.Runtime, manager, status)
	}
	
	return result, err
}

// validateExecuteParams validates the parameters for Execute and ExecuteStream
func (s *Service) validateExecuteParams(sandbox *types.Sandbox, execType, content string) error {
	// Validate sandbox
	if sandbox == nil || sandbox.Name == "" || sandbox.Namespace == "" {
		s.logger.Error("Invalid sandbox", nil, "sandbox", sandbox)
		return ErrInvalidSandbox
	}
	
	// Validate execution type
	if execType != "code" && execType != "command" {
		s.logger.Error("Invalid execution type", nil, "type", execType)
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
