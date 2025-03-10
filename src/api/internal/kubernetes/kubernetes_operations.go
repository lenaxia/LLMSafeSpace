package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecuteInSandbox executes code or a command in a sandbox
func (c *Client) ExecuteInSandbox(ctx context.Context, namespace, name string, req *types.ExecutionRequest) (*types.ExecutionResult, error) {
	// Get the sandbox to find the pod name
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return nil, fmt.Errorf("sandbox pod not found")
	}

	// Generate a unique execution ID
	execID := fmt.Sprintf("exec-%s-%d", name, time.Now().UnixNano())
	
	// Prepare the command to execute
	var cmd []string
	if req.Type == "code" {
		// Determine the interpreter based on the runtime
		interpreter := "python3"
		if strings.HasPrefix(sandbox.Spec.Runtime, "nodejs") {
			interpreter = "node"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "ruby") {
			interpreter = "ruby"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "go") {
			interpreter = "go run"
		}

		// Create a temporary file for the code
		tempFile := fmt.Sprintf("/tmp/%s.tmp", execID)
		
		// First write the code to a file
		writeCmd := []string{
			"sh",
			"-c",
			fmt.Sprintf("cat > %s << 'EOL'\n%s\nEOL", tempFile, req.Content),
		}
		
		// Execute the write command
		_, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, writeCmd, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to write code to file: %w", err)
		}
		
		// Then execute the code file
		cmd = []string{
			"sh",
			"-c",
			fmt.Sprintf("%s %s; EXIT_CODE=$?; rm %s; exit $EXIT_CODE", interpreter, tempFile, tempFile),
		}
	} else {
		// For command execution, just run the command in a shell
		cmd = []string{
			"sh",
			"-c",
			req.Content,
		}
	}

	// Execute the command
	startTime := time.Now()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout: stdout,
		Stderr: stderr,
		Timeout: time.Duration(req.Timeout) * time.Second,
	})
	
	completedAt := time.Now()
	
	// Determine status based on exit code and error
	status := "completed"
	if err != nil {
		if exitCode == 0 {
			exitCode = 1 // Set non-zero exit code if there was an error but exit code is 0
		}
		status = "error"
	} else if exitCode != 0 {
		status = "failed"
	}

	return &ExecutionResult{
		ID:          execID,
		Status:      status,
		StartedAt:   startTime,
		CompletedAt: completedAt,
		ExitCode:    exitCode,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
	}, nil
}

// ExecOptions defines options for command execution
type ExecOptions struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Timeout time.Duration
}

// executeCommand executes a command in a pod
func (c *Client) executeCommand(ctx context.Context, namespace, podName string, command []string, options *ExecOptions) (int, error) {
	if options == nil {
		options = &ExecOptions{}
	}
	
	// Create the exec request
	req := c.Clientset().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdin:   options.Stdin != nil,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	// Create the SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(c.RESTConfig(), "POST", req.URL())
	if err != nil {
		return 1, fmt.Errorf("failed to create executor: %w", err)
	}

	// If a timeout is specified, create a context with timeout
	var execCtx context.Context
	var cancel context.CancelFunc
	if options.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	} else {
		execCtx = ctx
	}

	// Execute the command
	err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdin:  options.Stdin,
		Stdout: options.Stdout,
		Stderr: options.Stderr,
		Tty:    false,
	})

	// Handle the error and extract exit code
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(remotecommand.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else if execCtx.Err() == context.DeadlineExceeded {
			return 124, fmt.Errorf("command timed out after %v", options.Timeout)
		} else {
			return 1, fmt.Errorf("failed to execute command: %w", err)
		}
	}

	return exitCode, nil
}

// ExecuteStreamInSandbox executes code or a command in a sandbox and streams the output
func (c *Client) ExecuteStreamInSandbox(
	ctx context.Context,
	namespace, name string,
	req *types.ExecutionRequest,
	outputCallback func(stream, content string),
) (*types.ExecutionResult, error) {
	// Get the sandbox to find the pod name
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return nil, fmt.Errorf("sandbox pod not found")
	}

	// Generate a unique execution ID
	execID := fmt.Sprintf("exec-%s-%d", name, time.Now().UnixNano())
	
	// Prepare the command to execute
	var cmd []string
	if req.Type == "code" {
		// Determine the interpreter based on the runtime
		interpreter := "python3"
		if strings.HasPrefix(sandbox.Spec.Runtime, "nodejs") {
			interpreter = "node"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "ruby") {
			interpreter = "ruby"
		} else if strings.HasPrefix(sandbox.Spec.Runtime, "go") {
			interpreter = "go run"
		}

		// Create a temporary file for the code
		tempFile := fmt.Sprintf("/tmp/%s.tmp", execID)
		
		// First write the code to a file
		writeCmd := []string{
			"sh",
			"-c",
			fmt.Sprintf("cat > %s << 'EOL'\n%s\nEOL", tempFile, req.Content),
		}
		
		// Execute the write command
		_, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, writeCmd, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to write code to file: %w", err)
		}
		
		// Then execute the code file
		cmd = []string{
			"sh",
			"-c",
			fmt.Sprintf("%s %s; EXIT_CODE=$?; rm %s; exit $EXIT_CODE", interpreter, tempFile, tempFile),
		}
	} else {
		// For command execution, just run the command in a shell
		cmd = []string{
			"sh",
			"-c",
			req.Content,
		}
	}

	// Execute the command with streaming
	startTime := time.Now()
	
	// Create streaming buffers
	stdoutStreamer := &streamWriter{stream: "stdout", callback: outputCallback}
	stderrStreamer := &streamWriter{stream: "stderr", callback: outputCallback}
	
	// Collect full output for the result
	stdoutCollector := &bytes.Buffer{}
	stderrCollector := &bytes.Buffer{}
	
	// Create multi-writers to both stream and collect output
	stdout := io.MultiWriter(stdoutStreamer, stdoutCollector)
	stderr := io.MultiWriter(stderrStreamer, stderrCollector)
	
	exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout: stdout,
		Stderr: stderr,
		Timeout: time.Duration(req.Timeout) * time.Second,
	})
	
	completedAt := time.Now()
	
	// Determine status based on exit code and error
	status := "completed"
	if err != nil {
		if exitCode == 0 {
			exitCode = 1 // Set non-zero exit code if there was an error but exit code is 0
		}
		status = "error"
	} else if exitCode != 0 {
		status = "failed"
	}

	return &ExecutionResult{
		ID:          execID,
		Status:      status,
		StartedAt:   startTime,
		CompletedAt: completedAt,
		ExitCode:    exitCode,
		Stdout:      stdoutCollector.String(),
		Stderr:      stderrCollector.String(),
	}, nil
}

// streamWriter implements io.Writer to stream output to a callback
type streamWriter struct {
	stream   string
	callback func(stream, content string)
	buffer   bytes.Buffer
}

// Write implements io.Writer
func (w *streamWriter) Write(p []byte) (n int, err error) {
	n, err = w.buffer.Write(p)
	if err != nil {
		return n, err
	}
	
	// Process the buffer line by line
	for {
		line, err := w.buffer.ReadString('\n')
		if err == io.EOF {
			// Put the incomplete line back in the buffer
			w.buffer.WriteString(line)
			break
		}
		if err != nil {
			return n, err
		}
		
		// Send the line to the callback
		w.callback(w.stream, line)
	}
	
	return n, nil
}

// ListFilesInSandbox lists files in a sandbox
func (c *Client) ListFilesInSandbox(ctx context.Context, namespace, name string, req *types.FileRequest) (*types.FileList, error) {
	// Get the sandbox to find the pod name
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return nil, fmt.Errorf("sandbox pod not found")
	}
	
	// Default to workspace directory if path is empty
	path := req.Path
	if path == "" {
		path = "/workspace"
	}
	
	// Create command to list files with details
	cmd := []string{
		"sh",
		"-c",
		fmt.Sprintf("find %s -maxdepth 1 -printf '%%p|%%s|%%y|%%T@|%%C@\\n'", path),
	}
	
	// Execute the command
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout: stdout,
		Stderr: stderr,
		Timeout: 10 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return nil, fmt.Errorf("failed to list files: %v (exit code: %d, stderr: %s)", 
			err, exitCode, stderr.String())
	}
	
	// Parse the output
	output := stdout.String()
	lines := strings.Split(output, "\n")
	
	files := make([]FileInfo, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		
		parts := strings.Split(line, "|")
		if len(parts) != 5 {
			continue
		}
		
		filePath := parts[0]
		
		// Skip the directory itself when listing its contents
		if filePath == path {
			continue
		}
		
		size, _ := parseInt64(parts[1])
		isDir := parts[2] == "d"
		
		// Parse timestamps
		createdAtUnix, _ := parseFloat64(parts[3])
		updatedAtUnix, _ := parseFloat64(parts[4])
		
		createdAt := time.Unix(int64(createdAtUnix), 0)
		updatedAt := time.Unix(int64(updatedAtUnix), 0)
		
		files = append(files, FileInfo{
			Path:      filePath,
			Size:      size,
			IsDir:     isDir,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}
	
	return &FileList{Files: files}, nil
}

// parseInt64 parses a string to int64
func parseInt64(s string) (int64, error) {
	var i int64
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// parseFloat64 parses a string to float64
func parseFloat64(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// DownloadFileFromSandbox downloads a file from a sandbox
func (c *Client) DownloadFileFromSandbox(ctx context.Context, namespace, name string, req *types.FileRequest) ([]byte, error) {
	// Get the sandbox to find the pod name
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return nil, fmt.Errorf("sandbox pod not found")
	}
	
	// Check if file exists and is not a directory
	checkCmd := []string{
		"sh",
		"-c",
		fmt.Sprintf("test -f %s && echo 'file' || (test -d %s && echo 'dir' || echo 'notfound')", req.Path, req.Path),
	}
	
	checkStdout := &bytes.Buffer{}
	checkStderr := &bytes.Buffer{}
	
	exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, checkCmd, &ExecOptions{
		Stdout: checkStdout,
		Stderr: checkStderr,
		Timeout: 5 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return nil, fmt.Errorf("failed to check file: %v (exit code: %d, stderr: %s)", 
			err, exitCode, checkStderr.String())
	}
	
	fileType := strings.TrimSpace(checkStdout.String())
	if fileType == "notfound" {
		return nil, fmt.Errorf("file not found: %s", req.Path)
	}
	if fileType == "dir" {
		return nil, fmt.Errorf("cannot download directory: %s", req.Path)
	}
	
	// Create command to read file
	cmd := []string{
		"cat",
		req.Path,
	}
	
	// Execute the command
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	exitCode, err = c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout: stdout,
		Stderr: stderr,
		Timeout: 30 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return nil, fmt.Errorf("failed to download file: %v (exit code: %d, stderr: %s)", 
			err, exitCode, stderr.String())
	}
	
	return stdout.Bytes(), nil
}

// UploadFileToSandbox uploads a file to a sandbox
func (c *Client) UploadFileToSandbox(ctx context.Context, namespace, name string, req *types.FileRequest) (*types.FileResult, error) {
	// Get the sandbox to find the pod name
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return nil, fmt.Errorf("sandbox pod not found")
	}
	
	// If this is a directory creation request
	if req.IsDir {
		// Create command to create directory
		cmd := []string{
			"mkdir",
			"-p",
			req.Path,
		}
		
		// Execute the command
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		
		exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
			Stdout: stdout,
			Stderr: stderr,
			Timeout: 10 * time.Second,
		})
		
		if err != nil || exitCode != 0 {
			return nil, fmt.Errorf("failed to create directory: %v (exit code: %d, stderr: %s)", 
				err, exitCode, stderr.String())
		}
		
		// Get directory info
		statCmd := []string{
			"sh",
			"-c",
			fmt.Sprintf("stat -c '%%s|%%Y|%%Z' %s", req.Path),
		}
		
		statStdout := &bytes.Buffer{}
		statStderr := &bytes.Buffer{}
		
		exitCode, err = c.executeCommand(ctx, namespace, sandbox.Status.PodName, statCmd, &ExecOptions{
			Stdout: statStdout,
			Stderr: statStderr,
			Timeout: 5 * time.Second,
		})
		
		if err != nil || exitCode != 0 {
			return nil, fmt.Errorf("failed to get directory info: %v (exit code: %d, stderr: %s)", 
				err, exitCode, statStderr.String())
		}
		
		// Parse stat output
		statOutput := strings.TrimSpace(statStdout.String())
		statParts := strings.Split(statOutput, "|")
		
		if len(statParts) != 3 {
			return nil, fmt.Errorf("invalid stat output: %s", statOutput)
		}
		
		size, _ := parseInt64(statParts[0])
		modTimeUnix, _ := parseInt64(statParts[1])
		changeTimeUnix, _ := parseInt64(statParts[2])
		
		modTime := time.Unix(modTimeUnix, 0)
		changeTime := time.Unix(changeTimeUnix, 0)
		
		return &FileResult{
			Path:      req.Path,
			Size:      size,
			IsDir:     true,
			CreatedAt: changeTime,
			UpdatedAt: modTime,
		}, nil
	}
	
	// Create parent directory if needed
	parentDir := filepath.Dir(req.Path)
	if parentDir != "/" {
		mkdirCmd := []string{
			"mkdir",
			"-p",
			parentDir,
		}
		
		_, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, mkdirCmd, &ExecOptions{
			Timeout: 5 * time.Second,
		})
		
		if err != nil {
			return nil, fmt.Errorf("failed to create parent directory: %w", err)
		}
	}
	
	// Create command to write file
	cmd := []string{
		"sh",
		"-c",
		fmt.Sprintf("cat > %s", req.Path),
	}
	
	// Execute the command
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdin:   bytes.NewReader(req.Content),
		Stdout:  stdout,
		Stderr:  stderr,
		Timeout: 30 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return nil, fmt.Errorf("failed to upload file: %v (exit code: %d, stderr: %s)", 
			err, exitCode, stderr.String())
	}
	
	// Get file info
	statCmd := []string{
		"sh",
		"-c",
		fmt.Sprintf("stat -c '%%s|%%Y|%%Z' %s", req.Path),
	}
	
	statStdout := &bytes.Buffer{}
	statStderr := &bytes.Buffer{}
	
	exitCode, err = c.executeCommand(ctx, namespace, sandbox.Status.PodName, statCmd, &ExecOptions{
		Stdout: statStdout,
		Stderr: statStderr,
		Timeout: 5 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return nil, fmt.Errorf("failed to get file info: %v (exit code: %d, stderr: %s)", 
			err, exitCode, statStderr.String())
	}
	
	// Parse stat output
	statOutput := strings.TrimSpace(statStdout.String())
	statParts := strings.Split(statOutput, "|")
	
	if len(statParts) != 3 {
		return nil, fmt.Errorf("invalid stat output: %s", statOutput)
	}
	
	size, _ := parseInt64(statParts[0])
	modTimeUnix, _ := parseInt64(statParts[1])
	changeTimeUnix, _ := parseInt64(statParts[2])
	
	modTime := time.Unix(modTimeUnix, 0)
	changeTime := time.Unix(changeTimeUnix, 0)
	
	return &FileResult{
		Path:      req.Path,
		Size:      size,
		IsDir:     false,
		CreatedAt: changeTime,
		UpdatedAt: modTime,
	}, nil
}

// DeleteFileInSandbox deletes a file in a sandbox
func (c *Client) DeleteFileInSandbox(ctx context.Context, namespace, name string, req *types.FileRequest) error {
	// Get the sandbox to find the pod name
	sandbox, err := c.LlmsafespaceV1().Sandboxes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get sandbox: %w", err)
	}

	if sandbox.Status.PodName == "" {
		return fmt.Errorf("sandbox pod not found")
	}
	
	// Check if path exists
	checkCmd := []string{
		"sh",
		"-c",
		fmt.Sprintf("test -e %s && echo 'exists' || echo 'notfound'", req.Path),
	}
	
	checkStdout := &bytes.Buffer{}
	checkStderr := &bytes.Buffer{}
	
	exitCode, err := c.executeCommand(ctx, namespace, sandbox.Status.PodName, checkCmd, &ExecOptions{
		Stdout: checkStdout,
		Stderr: checkStderr,
		Timeout: 5 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return fmt.Errorf("failed to check file: %v (exit code: %d, stderr: %s)", 
			err, exitCode, checkStderr.String())
	}
	
	fileExists := strings.TrimSpace(checkStdout.String())
	if fileExists == "notfound" {
		return fmt.Errorf("file not found: %s", req.Path)
	}
	
	// Create command to delete file
	cmd := []string{
		"rm",
		"-rf",
		req.Path,
	}
	
	// Execute the command
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	exitCode, err = c.executeCommand(ctx, namespace, sandbox.Status.PodName, cmd, &ExecOptions{
		Stdout: stdout,
		Stderr: stderr,
		Timeout: 10 * time.Second,
	})
	
	if err != nil || exitCode != 0 {
		return fmt.Errorf("failed to delete file: %v (exit code: %d, stderr: %s)", 
			err, exitCode, stderr.String())
	}
	
	return nil
}
