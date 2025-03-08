package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/api/internal/kubernetes"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/cache"
	"github.com/lenaxia/llmsafespace/api/internal/services/database"
	"github.com/lenaxia/llmsafespace/api/internal/services/execution"
	"github.com/lenaxia/llmsafespace/api/internal/services/file"
	"github.com/lenaxia/llmsafespace/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespace/api/internal/services/warmpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	llmsafespacev1 "github.com/lenaxia/llmsafespace/api/internal/kubernetes/apis/llmsafespace/v1"
)

// Service handles sandbox operations
type Service struct {
	logger        *logger.Logger
	k8sClient     *kubernetes.Client
	dbService     *database.Service
	warmPoolSvc   *warmpool.Service
	fileSvc       *file.Service
	executionSvc  *execution.Service
	metricsSvc    *metrics.Service
	sessionMgr    *SessionManager
}

// CreateSandboxRequest defines the request for creating a sandbox
type CreateSandboxRequest struct {
	Runtime       string                        `json:"runtime"`
	SecurityLevel string                        `json:"securityLevel,omitempty"`
	Timeout       int                           `json:"timeout,omitempty"`
	Resources     *llmsafespacev1.ResourceRequirements `json:"resources,omitempty"`
	NetworkAccess *llmsafespacev1.NetworkAccess        `json:"networkAccess,omitempty"`
	UseWarmPool   bool                          `json:"useWarmPool,omitempty"`
	UserID        string                        `json:"-"`
	Namespace     string                        `json:"-"`
}

// ExecuteRequest defines the request for executing code or a command
type ExecuteRequest struct {
	Type      string `json:"type"`      // "code" or "command"
	Content   string `json:"content"`   // Code or command to execute
	Timeout   int    `json:"timeout"`   // Execution timeout in seconds
	SandboxID string `json:"-"`         // Set by the handler
}

// InstallPackagesRequest defines the request for installing packages
type InstallPackagesRequest struct {
	Packages  []string `json:"packages"` // Packages to install
	Manager   string   `json:"manager"`  // Package manager to use
	SandboxID string   `json:"-"`        // Set by the handler
}

// New creates a new sandbox service
func New(
	logger *logger.Logger,
	k8sClient *kubernetes.Client,
	dbService *database.Service,
	warmPoolSvc *warmpool.Service,
	fileSvc *file.Service,
	executionSvc *execution.Service,
	metricsSvc *metrics.Service,
	cacheService *cache.Service,
) (*Service, error) {
	return &Service{
		logger:       logger,
		k8sClient:    k8sClient,
		dbService:    dbService,
		warmPoolSvc:  warmPoolSvc,
		fileSvc:      fileSvc,
		executionSvc: executionSvc,
		metricsSvc:   metricsSvc,
		sessionMgr:   NewSessionManager(cacheService),
	}, nil
}

// CreateSandbox creates a new sandbox
func (s *Service) CreateSandbox(ctx context.Context, req CreateSandboxRequest) (*llmsafespacev1.Sandbox, error) {
	// Set default namespace if not provided
	if req.Namespace == "" {
		req.Namespace = "default"
	}

	// Set default security level if not provided
	if req.SecurityLevel == "" {
		req.SecurityLevel = "standard"
	}

	// Set default timeout if not provided
	if req.Timeout == 0 {
		req.Timeout = 300
	}

	// Create sandbox object
	sandbox := &llmsafespacev1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "sb-",
			Namespace:    req.Namespace,
			Labels: map[string]string{
				"app":     "llmsafespace",
				"user-id": req.UserID,
			},
			Annotations: map[string]string{},
		},
		Spec: llmsafespacev1.SandboxSpec{
			Runtime:       req.Runtime,
			SecurityLevel: req.SecurityLevel,
			Timeout:       req.Timeout,
			Resources:     req.Resources,
			NetworkAccess: req.NetworkAccess,
		},
	}

	// Check if warm pool should be used
	warmPodUsed := false
	if req.UseWarmPool {
		// Check if warm pods are available
		available, err := s.warmPoolSvc.CheckAvailability(ctx, req.Runtime, req.SecurityLevel)
		if err != nil {
			s.logger.Error("Failed to check warm pool availability", err,
				"runtime", req.Runtime,
				"security_level", req.SecurityLevel,
			)
		} else if available {
			// Add annotation to request warm pod
			if sandbox.Annotations == nil {
				sandbox.Annotations = make(map[string]string)
			}
			sandbox.Annotations["llmsafespace.dev/use-warm-pod"] = "true"
			sandbox.Annotations["llmsafespace.dev/warm-pod-runtime"] = req.Runtime
			sandbox.Annotations["llmsafespace.dev/warm-pod-security-level"] = req.SecurityLevel
			warmPodUsed = true
		}
	}

	// Create the sandbox in Kubernetes
	result, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Create(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox: %w", err)
	}

	// Store sandbox metadata in database
	err = s.dbService.CreateSandboxMetadata(ctx, result.Name, req.UserID, req.Runtime)
	if err != nil {
		// Attempt to clean up the Kubernetes resource
		_ = s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Delete(result.Name, metav1.DeleteOptions{})
		return nil, fmt.Errorf("failed to store sandbox metadata: %w", err)
	}

	// Record metrics
	s.metricsSvc.RecordSandboxCreation(req.Runtime, warmPodUsed)

	return result, nil
}

// GetSandbox gets a sandbox by ID
func (s *Service) GetSandbox(ctx context.Context, sandboxID string) (*llmsafespacev1.Sandbox, error) {
	// Get sandbox metadata from database
	metadata, err := s.dbService.GetSandboxMetadata(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox metadata: %w", err)
	}

	if metadata == nil {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	return sandbox, nil
}

// ListSandboxes lists sandboxes for a user
func (s *Service) ListSandboxes(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	// Get sandboxes from database
	sandboxes, err := s.dbService.ListSandboxes(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	// Enrich with Kubernetes data
	for i, sandbox := range sandboxes {
		id := sandbox["id"].(string)
		k8sSandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(id, metav1.GetOptions{})
		if err == nil {
			sandboxes[i]["status"] = k8sSandbox.Status.Phase
			sandboxes[i]["endpoint"] = k8sSandbox.Status.Endpoint
		}
	}

	return sandboxes, nil
}

// TerminateSandbox terminates a sandbox
func (s *Service) TerminateSandbox(ctx context.Context, sandboxID string) error {
	// Get sandbox metadata from database
	metadata, err := s.dbService.GetSandboxMetadata(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to get sandbox metadata: %w", err)
	}

	if metadata == nil {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Delete sandbox from Kubernetes
	err = s.k8sClient.LlmsafespaceV1().Sandboxes("default").Delete(sandboxID, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete sandbox: %w", err)
	}

	// Record metrics
	s.metricsSvc.RecordSandboxTermination(metadata["runtime"].(string))

	return nil
}

// GetSandboxStatus gets the status of a sandbox
func (s *Service) GetSandboxStatus(ctx context.Context, sandboxID string) (*llmsafespacev1.SandboxStatus, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	return &sandbox.Status, nil
}

// Execute executes code or a command in a sandbox
func (s *Service) Execute(ctx context.Context, req ExecuteRequest) (*execution.Result, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(req.SandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running: %s", req.SandboxID)
	}

	// Execute code or command
	startTime := time.Now()
	result, err := s.executionSvc.Execute(ctx, sandbox, req.Type, req.Content, req.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to execute: %w", err)
	}

	// Record metrics
	status := "success"
	if result.ExitCode != 0 {
		status = "failure"
	}
	s.metricsSvc.RecordExecution(req.Type, sandbox.Spec.Runtime, status, time.Since(startTime))

	return result, nil
}

// ListFiles lists files in a sandbox
func (s *Service) ListFiles(ctx context.Context, sandboxID, path string) ([]interfaces.FileInfo, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running: %s", sandboxID)
	}

	// List files
	files, err := s.fileSvc.ListFiles(ctx, sandbox, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	return files, nil
}

// DownloadFile downloads a file from a sandbox
func (s *Service) DownloadFile(ctx context.Context, sandboxID, path string) ([]byte, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running: %s", sandboxID)
	}

	// Download file
	content, err := s.fileSvc.DownloadFile(ctx, sandbox, path)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}

	return content, nil
}

// UploadFile uploads a file to a sandbox
func (s *Service) UploadFile(ctx context.Context, sandboxID, path string, content []byte) (*interfaces.FileInfo, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running: %s", sandboxID)
	}

	// Upload file
	fileInfo, err := s.fileSvc.UploadFile(ctx, sandbox, path, content)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	return fileInfo, nil
}

// DeleteFile deletes a file from a sandbox
func (s *Service) DeleteFile(ctx context.Context, sandboxID, path string) error {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return fmt.Errorf("sandbox is not running: %s", sandboxID)
	}

	// Delete file
	err = s.fileSvc.DeleteFile(ctx, sandbox, path)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// InstallPackages installs packages in a sandbox
func (s *Service) InstallPackages(ctx context.Context, req InstallPackagesRequest) (*execution.Result, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(req.SandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running: %s", req.SandboxID)
	}

	// Install packages
	result, err := s.executionSvc.InstallPackages(ctx, sandbox, req.Packages, req.Manager)
	if err != nil {
		return nil, fmt.Errorf("failed to install packages: %w", err)
	}

	return result, nil
}

// CreateSession creates a new WebSocket session
func (s *Service) CreateSession(userID, sandboxID string, conn *websocket.Conn) (*Session, error) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(sandboxID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox: %w", err)
	}

	// Check if sandbox is running
	if sandbox.Status.Phase != "Running" {
		return nil, fmt.Errorf("sandbox is not running: %s", sandboxID)
	}

	// Create session
	session := s.sessionMgr.CreateSession(userID, sandboxID, conn)

	// Increment active connections metric
	s.metricsSvc.IncrementActiveConnections("websocket")

	return session, nil
}

// CloseSession closes a WebSocket session
func (s *Service) CloseSession(sessionID string) {
	s.sessionMgr.CloseSession(sessionID)

	// Decrement active connections metric
	s.metricsSvc.DecrementActiveConnections("websocket")
}

// HandleSession handles a WebSocket session
func (s *Service) HandleSession(session *Session) {
	// Get sandbox from Kubernetes
	sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes("default").Get(session.SandboxID, metav1.GetOptions{})
	if err != nil {
		session.SendError("sandbox_not_found", "Failed to get sandbox")
		return
	}

	// Handle messages
	for {
		messageType, p, err := session.Conn.ReadMessage()
		if err != nil {
			break
		}

		// Only handle text messages
		if messageType != websocket.TextMessage {
			continue
		}

		// Parse message
		msg, err := ParseMessage(p)
		if err != nil {
			session.SendError("invalid_message", "Failed to parse message")
			continue
		}

		// Handle message based on type
		switch msg.Type {
		case "execute":
			s.handleExecuteMessage(session, sandbox, msg)
		case "cancel":
			s.handleCancelMessage(session, msg)
		case "ping":
			session.Send(Message{
				Type:      "pong",
				Timestamp: time.Now().UnixMilli(),
			})
		default:
			session.SendError("unknown_message_type", "Unknown message type")
		}
	}
}

// handleExecuteMessage handles an execute message
func (s *Service) handleExecuteMessage(session *Session, sandbox *llmsafespacev1.Sandbox, msg Message) {
	// Get execution parameters
	executionID, _ := msg.GetString("executionId")
	mode, _ := msg.GetString("mode")
	content, _ := msg.GetString("content")
	timeout, _ := msg.GetInt("timeout")

	if executionID == "" || (mode != "code" && mode != "command") || content == "" {
		session.SendError("invalid_request", "Invalid execution request")
		return
	}

	// Set default timeout if not provided
	if timeout <= 0 {
		timeout = 30
	}

	// Execute in goroutine
	go func() {
		// Create execution context with cancellation
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		// Store cancellation function in session
		session.SetCancellationFunc(executionID, cancel)

		// Notify client that execution has started
		session.Send(Message{
			Type:        "execution_start",
			ExecutionID: executionID,
			Timestamp:   time.Now().UnixMilli(),
		})

		// Execute code or command
		startTime := time.Now()
		result, err := s.executionSvc.ExecuteStream(ctx, sandbox, mode, content, timeout, func(stream, content string) {
			session.Send(Message{
				Type:        "output",
				ExecutionID: executionID,
				Stream:      stream,
				Content:     content,
				Timestamp:   time.Now().UnixMilli(),
			})
		})

		// Remove cancellation function
		session.RemoveCancellationFunc(executionID)

		// Handle execution result
		if err != nil {
			session.Send(Message{
				Type:        "error",
				Code:        "execution_failed",
				Message:     err.Error(),
				ExecutionID: executionID,
				Timestamp:   time.Now().UnixMilli(),
			})
			return
		}

		// Notify client that execution has completed
		session.Send(Message{
			Type:        "execution_complete",
			ExecutionID: executionID,
			ExitCode:    result.ExitCode,
			Timestamp:   time.Now().UnixMilli(),
		})

		// Record metrics
		status := "success"
		if result.ExitCode != 0 {
			status = "failure"
		}
		s.metricsSvc.RecordExecution(mode, sandbox.Spec.Runtime, status, time.Since(startTime))
	}()
}

// handleCancelMessage handles a cancel message
func (s *Service) handleCancelMessage(session *Session, msg Message) {
	// Get execution ID
	executionID, _ := msg.GetString("executionId")
	if executionID == "" {
		session.SendError("invalid_request", "Missing executionId")
		return
	}

	// Cancel execution
	if cancelled := session.CancelExecution(executionID); cancelled {
		session.Send(Message{
			Type:        "execution_cancelled",
			ExecutionID: executionID,
			Timestamp:   time.Now().UnixMilli(),
		})
	}
}
