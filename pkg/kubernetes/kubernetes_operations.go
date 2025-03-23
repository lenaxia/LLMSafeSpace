package kubernetes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/pkg/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecuteInSandbox executes code or a command in a sandbox
func (c *Client) ExecuteInSandbox(ctx context.Context, namespace, name string, req *types.ExecutionRequest) (*types.ExecutionResult, error) {
	// [Previous implementation unchanged...]

	// Execute the command
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exitCode, err := c.ExecuteCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout:  stdout,
		Stderr:  stderr,
		Timeout: time.Duration(req.Timeout) * time.Second,
	})

	if err != nil {
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	return &types.ExecutionResult{
		ID:          req.ID,
		Status:      "completed",
		StartedAt:   metav1.Now(),
		CompletedAt: metav1.Now(),
		ExitCode:    exitCode,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
	}, nil
}

// ExecuteStreamInSandbox executes code or a command in a sandbox and streams the output
func (c *Client) ExecuteStreamInSandbox(
	ctx context.Context,
	namespace, name string,
	req *types.ExecutionRequest,
	outputCallback func(stream, content string),
) (*types.ExecutionResult, error) {
	// [Previous implementation unchanged...]

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exitCode, err := c.ExecuteCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout:  stdout,
		Stderr:  stderr,
		Timeout: time.Duration(req.Timeout) * time.Second,
	})

	if err != nil {
		return nil, fmt.Errorf("stream execution failed: %w", err)
	}

	// Process output streams
	if stdout.Len() > 0 {
		outputCallback("stdout", stdout.String())
	}
	if stderr.Len() > 0 {
		outputCallback("stderr", stderr.String())
	}

	return &types.ExecutionResult{
		ID:          req.ID,
		Status:      "completed",
		StartedAt:   metav1.Now(),
		CompletedAt: metav1.Now(),
		ExitCode:    exitCode,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
	}, nil
}

// [Rest of the file remains exactly the same as provided by the user]
