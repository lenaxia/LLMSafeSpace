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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecOptions holds configuration for command execution
type ExecOptions struct {
	Command []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Timeout time.Duration
}

// ExecuteCommand executes a command in a pod and returns exit code and error
func (c *Client) ExecuteCommand(ctx context.Context, namespace, podName string, command []string, opts *ExecOptions) (int, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       false,
			Container: "sandbox",
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return -1, fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    false,
	})

	if err != nil {
		if exitErr, ok := err.(remotecommand.ExitError); ok {
			return exitErr.ExitStatus(), nil
		}
		return -1, fmt.Errorf("stream error: %w", err)
	}

	return 0, nil
}

// getSandbox retrieves the sandbox resource and associated pod
func (c *Client) getSandbox(ctx context.Context, namespace, name string) (*types.Sandbox, *corev1.Pod, error) {
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return nil, nil, fmt.Errorf("sandbox pod not yet allocated")
	}

	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, sandbox.Status.PodName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get pod: %w", err)
	}

	if pod.Status.Phase != corev1.PodRunning {
		return nil, nil, fmt.Errorf("pod is not in running state (current phase: %s)", pod.Status.Phase)
	}

	return sandbox, pod, nil
}

// ExecuteInSandbox executes code or a command in a sandbox
func (c *Client) ExecuteInSandbox(ctx context.Context, namespace, name string, req *types.ExecutionRequest) (*types.ExecutionResult, error) {
	sandbox, pod, err := c.getSandbox(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("sandbox validation failed: %w", err)
	}

	var cmd []string
	switch req.Type {
	case "code":
		// Create temporary file and execute it
		tempFile := fmt.Sprintf("/tmp/llm_exec_%d.sh", time.Now().UnixNano())
		cmd = []string{"/bin/sh", "-c", fmt.Sprintf(
			"echo %s | base64 -d > %s && chmod +x %s && %s",
			base64.StdEncoding.EncodeToString([]byte(req.Content)),
			tempFile,
			tempFile,
			tempFile,
		)}
	case "command":
		cmd = []string{"/bin/sh", "-c", req.Content}
	default:
		return nil, fmt.Errorf("invalid execution type: %s", req.Type)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	exitCode, err := c.ExecuteCommand(ctx, namespace, pod.Name, cmd, &ExecOptions{
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
	sandbox, pod, err := c.getSandbox(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("sandbox validation failed: %w", err)
	}

	var cmd []string
	switch req.Type {
	case "code":
		tempFile := fmt.Sprintf("/tmp/llm_exec_%d.sh", time.Now().UnixNano())
		cmd = []string{"/bin/sh", "-c", fmt.Sprintf(
			"echo %s | base64 -d > %s && chmod +x %s && %s",
			base64.StdEncoding.EncodeToString([]byte(req.Content)),
			tempFile,
			tempFile,
			tempFile,
		)}
	case "command":
		cmd = []string{"/bin/sh", "-c", req.Content}
	default:
		return nil, fmt.Errorf("invalid execution type: %s", req.Type)
	}

	stdout := &streamWriter{callback: func(b []byte) { outputCallback("stdout", string(b)) }}
	stderr := &streamWriter{callback: func(b []byte) { outputCallback("stderr", string(b)) }}

	exitCode, err := c.ExecuteCommand(ctx, namespace, pod.Name, cmd, &ExecOptions{
		Stdout:  stdout,
		Stderr:  stderr,
		Timeout: time.Duration(req.Timeout) * time.Second,
	})

	if err != nil {
		return nil, fmt.Errorf("stream execution failed: %w", err)
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

// streamWriter implements io.Writer for streaming output
type streamWriter struct {
	buffer   bytes.Buffer
	callback func([]byte)
}

func (w *streamWriter) Write(p []byte) (n int, err error) {
	w.buffer.Write(p)
	w.callback(p)
	return len(p), nil
}

func (w *streamWriter) String() string {
	return w.buffer.String()
}
