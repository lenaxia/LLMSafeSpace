package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Service handles code and command execution
type Service struct {
	logger    *logger.Logger
	k8sClient interfaces.KubernetesClient
}

// Result represents the result of code or command execution
type Result struct {
	ExecutionID  string    `json:"executionId"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"startedAt"`
	CompletedAt  time.Time `json:"completedAt"`
	ExitCode     int       `json:"exitCode"`
	Stdout       string    `json:"stdout"`
	Stderr       string    `json:"stderr"`
}

// New creates a new execution service
func New(logger *logger.Logger, k8sClient interfaces.KubernetesClient) (*Service, error) {
	return &Service{
		logger:    logger,
		k8sClient: k8sClient,
	}, nil
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, sandbox *llmsafespacev1.Sandbox, execType, content string, timeout int) (*Result, error) {
	// Create execution request
	execReq := &kubernetes.ExecutionRequest{
		Type:    execType,
		Content: content,
		Timeout: timeout,
	}

	// Execute code via Kubernetes API
	execResult, err := s.k8sClient.ExecuteInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute in sandbox: %w", err)
	}

	// Return execution result
	return &Result{
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
	sandbox *llmsafespacev1.Sandbox,
	execType, content string,
	timeout int,
	outputCallback func(stream, content string),
) (*Result, error) {
	// Create execution request
	execReq := &kubernetes.ExecutionRequest{
		Type:    execType,
		Content: content,
		Timeout: timeout,
		Stream:  true,
	}

	// Execute code via Kubernetes API
	execResult, err := s.k8sClient.ExecuteStreamInSandbox(ctx, sandbox.Namespace, sandbox.Name, execReq, outputCallback)
	if err != nil {
		return nil, fmt.Errorf("failed to execute stream in sandbox: %w", err)
	}

	// Return execution result
	return &Result{
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
func (s *Service) InstallPackages(ctx context.Context, sandbox *llmsafespacev1.Sandbox, packages []string, manager string) (*Result, error) {
	// Determine package manager command
	var cmd string
	if manager == "" {
		// Auto-detect package manager based on runtime
		if sandbox.Spec.Runtime == "python" || sandbox.Spec.Runtime == "python:3.10" {
			manager = "pip"
		} else if sandbox.Spec.Runtime == "nodejs" || sandbox.Spec.Runtime == "nodejs:18" {
			manager = "npm"
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
	default:
		return nil, fmt.Errorf("unsupported package manager: %s", manager)
	}

	// Execute command
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
