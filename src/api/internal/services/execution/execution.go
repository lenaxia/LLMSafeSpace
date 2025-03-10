package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Service handles code and command execution
type Service struct {
	logger    *logger.Logger
	k8sClient kubernetes.KubernetesClient
}

// Ensure Service implements interfaces.ExecutionService
var _ interfaces.ExecutionService = &Service{}

// Ensure Service implements interfaces.ExecutionService
var _ interfaces.ExecutionService = (*Service)(nil)

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
func New(logger *logger.Logger, k8sClient kubernetes.KubernetesClient) (*Service, error) {
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
	}, nil
}

// ExecuteCode executes code in a sandbox
func (s *Service) ExecuteCode(ctx context.Context, sandboxID, code string, timeout int) (*interfaces.Result, error) {
	s.logger.Debug("Executing code in sandbox", "sandbox_id", sandboxID, "timeout", timeout)
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxID,
			Namespace: "default", // Use default namespace if not specified
		},
	}
	return s.Execute(ctx, sandbox, "code", code, timeout)
}

// ExecuteCommand executes a command in a sandbox
func (s *Service) ExecuteCommand(ctx context.Context, sandboxID, command string, timeout int) (*interfaces.Result, error) {
	s.logger.Debug("Executing command in sandbox", "sandbox_id", sandboxID, "timeout", timeout)
	sandbox := &types.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxID,
			Namespace: "default", // Use default namespace if not specified
		},
	}
	return s.Execute(ctx, sandbox, "command", command, timeout)
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, sandbox *types.Sandbox, execType, content string, timeout int) (*interfaces.Result, error) {
	startTime := time.Now()
	s.logger.Debug("Executing in sandbox", 
		"namespace", sandbox.Namespace,
		"name", sandbox.Name,
		"type", execType,
		"timeout", timeout)

	// Set default timeout if not specified
	if timeout <= 0 {
		timeout = 30 // Default timeout of 30 seconds
	}

	// Create execution request
	execReq := &types.ExecutionRequest{
		Type:    execType,
		Content: content,
		Timeout: timeout,
	}

	// Execute code via Kubernetes API
	execResult, err := s.k8sClient.ExecuteInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq)
	if err != nil {
		s.logger.Error("Failed to execute in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name)
		return nil, fmt.Errorf("failed to execute in sandbox: %w", err)
	}

	duration := time.Since(startTime)
	s.logger.Debug("Execution completed", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"duration_ms", duration.Milliseconds(), 
		"exit_code", execResult.ExitCode)

	// Return execution result
	return &interfaces.Result{
		ExecutionID:  execResult.ID,
		Status:       execResult.Status,
		StartedAt:    execResult.StartedAt,
		CompletedAt:  execResult.CompletedAt,
		ExitCode:     execResult.ExitCode,
		Stdout:       execResult.Stdout,
		Stderr:       execResult.Stderr,
	}, nil
}

// ExecuteStream executes code or a command in a sandbox and streams the output
func (s *Service) ExecuteStream(
	ctx context.Context,
	sandbox *types.Sandbox,
	execType, content string,
	timeout int,
	outputCallback func(stream, content string),
) (*interfaces.Result, error) {
	startTime := time.Now()
	s.logger.Debug("Executing stream in sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"type", execType, 
		"timeout", timeout)

	// Set default timeout if not specified
	if timeout <= 0 {
		timeout = 30 // Default timeout of 30 seconds
	}

	// Create execution request
	execReq := &types.ExecutionRequest{
		Type:    execType,
		Content: content,
		Timeout: timeout,
		Stream:  true,
	}

	// Execute code via Kubernetes API
	execResult, err := s.k8sClient.ExecuteStreamInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq, outputCallback)
	if err != nil {
		s.logger.Error("Failed to execute stream in sandbox", err, 
			"namespace", sandbox.Namespace, 
			"name", sandbox.Name)
		return nil, fmt.Errorf("failed to execute stream in sandbox: %w", err)
	}

	duration := time.Since(startTime)
	s.logger.Debug("Stream execution completed", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"duration_ms", duration.Milliseconds(), 
		"exit_code", execResult.ExitCode)

	// Return execution result
	return &interfaces.Result{
		ExecutionID:  execResult.ID,
		Status:       execResult.Status,
		StartedAt:    execResult.StartedAt,
		CompletedAt:  execResult.CompletedAt,
		ExitCode:     execResult.ExitCode,
		Stdout:       execResult.Stdout,
		Stderr:       execResult.Stderr,
	}, nil
}

// InstallPackages installs packages in a sandbox
func (s *Service) InstallPackages(ctx context.Context, sandbox *types.Sandbox, packages []string, manager string) (*interfaces.Result, error) {
	if len(packages) == 0 {
		return nil, fmt.Errorf("no packages specified for installation")
	}

	s.logger.Info("Installing packages in sandbox", 
		"namespace", sandbox.Namespace, 
		"name", sandbox.Name, 
		"packages", packages, 
		"manager", manager)

	// Determine package manager command
	var cmd string
	if manager == "" {
		// Auto-detect package manager based on runtime
		if sandbox.Spec.Runtime == "python" || sandbox.Spec.Runtime == "python:3.10" {
			manager = "pip"
		} else if sandbox.Spec.Runtime == "nodejs" || sandbox.Spec.Runtime == "nodejs:18" {
			manager = "npm"
		} else if sandbox.Spec.Runtime == "ruby" || sandbox.Spec.Runtime == "ruby:3.1" {
			manager = "gem"
		} else if sandbox.Spec.Runtime == "go" || sandbox.Spec.Runtime == "go:1.18" {
			manager = "go"
		} else {
			return nil, fmt.Errorf("unable to determine package manager for runtime: %s", sandbox.Spec.Runtime)
		}
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
		return nil, fmt.Errorf("unsupported package manager: %s", manager)
	}

	// Execute command with extended timeout for package installation
	return s.Execute(ctx, sandbox, "command", cmd, 300)
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
