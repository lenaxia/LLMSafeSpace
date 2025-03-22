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
	startTime := metav1.Now()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exitCode, err := c.ExecuteCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout:  stdout,
		Stderr:  stderr,
		Timeout: metav1.Duration{Duration: req.Timeout * time.Second}, // Fixed duration initialization
	})

	// [Remainder of function unchanged...]
}

// ExecuteStreamInSandbox executes code or a command in a sandbox and streams the output
func (c *Client) ExecuteStreamInSandbox(
	ctx context.Context,
	namespace, name string,
	req *types.ExecutionRequest,
	outputCallback func(stream, content string),
) (*types.ExecutionResult, error) {
	// [Previous implementation unchanged...]

	exitCode, err := c.ExecuteCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout:  stdout,
		Stderr:  stderr,
		Timeout: metav1.Duration{Duration: req.Timeout}, // Fixed duration initialization
	})

	// [Remainder of function unchanged...]
}

// [Rest of the file remains exactly the same as provided by the user]
