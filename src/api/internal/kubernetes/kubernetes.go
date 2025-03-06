package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Client manages Kubernetes API interactions
type Client struct {
	clientset         kubernetes.Interface
	llmsafespaceClient *LLMSafespaceV1Client
	restConfig        *rest.Config
	informerFactory   *InformerFactory
	logger            *logger.Logger
	config            *config.Config
	stopCh            chan struct{}
}

// ExecutionRequest defines a request to execute code or a command
type ExecutionRequest struct {
	Type    string `json:"type"`    // "code" or "command"
	Content string `json:"content"` // Code or command to execute
	Timeout int    `json:"timeout"` // Execution timeout in seconds
	Stream  bool   `json:"stream"`  // Whether to stream the output
}

// ExecutionResult defines the result of code or command execution
type ExecutionResult struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	ExitCode   int       `json:"exitCode"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
}

// FileRequest defines a request to perform a file operation
type FileRequest struct {
	Path    string `json:"path"`
	Content []byte `json:"content,omitempty"`
}

// FileResult defines the result of a file operation
type FileResult struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	IsDir     bool      `json:"isDir"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FileListResult defines the result of listing files
type FileListResult struct {
	Files []FileResult `json:"files"`
}

// New creates a new Kubernetes client
func New(cfg *config.Config, log *logger.Logger) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if cfg.Kubernetes.InCluster {
		// Use in-cluster config
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
		}
		log.Info("Using in-cluster Kubernetes configuration")
	} else {
		// Use kubeconfig file
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.Kubernetes.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
		log.Info("Using external Kubernetes configuration", "path", cfg.Kubernetes.ConfigPath)
	}

	// Configure connection pooling
	restConfig.QPS = 100
	restConfig.Burst = 200
	restConfig.Timeout = time.Second * 30

	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Create LLMSafespace client
	llmsafespaceClient, err := newLLMSafespaceV1Client(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLMSafespace client: %w", err)
	}

	// Create informer factory
	informerFactory := NewInformerFactory(llmsafespaceClient, time.Minute*30, cfg.Kubernetes.Namespace)

	return &Client{
		clientset:         clientset,
		llmsafespaceClient: llmsafespaceClient,
		restConfig:        restConfig,
		informerFactory:   informerFactory,
		logger:            log,
		config:            cfg,
		stopCh:            make(chan struct{}),
	}, nil
}

// Start starts the Kubernetes client
func (c *Client) Start() error {
	// Start informer factory
	c.informerFactory.StartInformers(c.stopCh)
	c.logger.Info("Started informer factory")

	// Configure leader election if enabled
	if c.config.Kubernetes.LeaderElection.Enabled {
		go c.runLeaderElection()
	}

	return nil
}

// Stop stops the Kubernetes client
func (c *Client) Stop() {
	close(c.stopCh)
	c.logger.Info("Stopped Kubernetes client")
}

// runLeaderElection starts the leader election process
func (c *Client) runLeaderElection() {
	// Create leader election config
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "llmsafespace-api-leader",
			Namespace: c.config.Kubernetes.Namespace,
		},
		Client: c.clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: c.config.Kubernetes.PodName,
		},
	}

	// Configure leader election
	leaderelection.RunOrDie(context.Background(), leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   c.config.Kubernetes.LeaderElection.LeaseDuration,
		RenewDeadline:   c.config.Kubernetes.LeaderElection.RenewDeadline,
		RetryPeriod:     c.config.Kubernetes.LeaderElection.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				c.logger.Info("Started leading")
			},
			OnStoppedLeading: func() {
				c.logger.Info("Stopped leading")
			},
			OnNewLeader: func(identity string) {
				c.logger.Info("New leader elected", "leader", identity)
			},
		},
	})
}

// Clientset returns the Kubernetes clientset
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}

// LlmsafespaceV1 returns the LLMSafespace v1 client
func (c *Client) LlmsafespaceV1() *LLMSafespaceV1Client {
	return c.llmsafespaceClient
}

// RESTConfig returns the REST config
func (c *Client) RESTConfig() *rest.Config {
	return c.restConfig
}

// ExecuteInSandbox executes code or a command in a sandbox
func (c *Client) ExecuteInSandbox(ctx context.Context, namespace, name string, req *ExecutionRequest) (*ExecutionResult, error) {
	// TODO: Implement actual execution via Kubernetes API
	// This is a placeholder implementation
	return &ExecutionResult{
		ID:         "exec-123",
		Status:     "completed",
		StartedAt:  time.Now().Add(-1 * time.Second),
		CompletedAt: time.Now(),
		ExitCode:   0,
		Stdout:     "Hello, world!",
		Stderr:     "",
	}, nil
}

// ExecuteStreamInSandbox executes code or a command in a sandbox and streams the output
func (c *Client) ExecuteStreamInSandbox(
	ctx context.Context,
	namespace, name string,
	req *ExecutionRequest,
	outputCallback func(stream, content string),
) (*ExecutionResult, error) {
	// TODO: Implement actual streaming execution via Kubernetes API
	// This is a placeholder implementation
	outputCallback("stdout", "Hello, ")
	time.Sleep(100 * time.Millisecond)
	outputCallback("stdout", "world!")
	time.Sleep(100 * time.Millisecond)
	outputCallback("stdout", "\n")

	return &ExecutionResult{
		ID:         "exec-123",
		Status:     "completed",
		StartedAt:  time.Now().Add(-1 * time.Second),
		CompletedAt: time.Now(),
		ExitCode:   0,
		Stdout:     "Hello, world!\n",
		Stderr:     "",
	}, nil
}

// ListFilesInSandbox lists files in a sandbox
func (c *Client) ListFilesInSandbox(ctx context.Context, namespace, name string, req *FileRequest) (*FileListResult, error) {
	// TODO: Implement actual file listing via Kubernetes API
	// This is a placeholder implementation
	return &FileListResult{
		Files: []FileResult{
			{
				Path:      "/workspace/file1.txt",
				Size:      100,
				IsDir:     false,
				CreatedAt: time.Now().Add(-1 * time.Hour),
				UpdatedAt: time.Now().Add(-30 * time.Minute),
			},
			{
				Path:      "/workspace/dir1",
				Size:      0,
				IsDir:     true,
				CreatedAt: time.Now().Add(-2 * time.Hour),
				UpdatedAt: time.Now().Add(-2 * time.Hour),
			},
		},
	}, nil
}

// DownloadFileFromSandbox downloads a file from a sandbox
func (c *Client) DownloadFileFromSandbox(ctx context.Context, namespace, name string, req *FileRequest) ([]byte, error) {
	// TODO: Implement actual file download via Kubernetes API
	// This is a placeholder implementation
	return []byte("Hello, world!"), nil
}

// UploadFileToSandbox uploads a file to a sandbox
func (c *Client) UploadFileToSandbox(ctx context.Context, namespace, name string, req *FileRequest) (*FileResult, error) {
	// TODO: Implement actual file upload via Kubernetes API
	// This is a placeholder implementation
	return &FileResult{
		Path:      req.Path,
		Size:      int64(len(req.Content)),
		IsDir:     false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

// DeleteFileInSandbox deletes a file in a sandbox
func (c *Client) DeleteFileInSandbox(ctx context.Context, namespace, name string, req *FileRequest) error {
	// TODO: Implement actual file deletion via Kubernetes API
	// This is a placeholder implementation
	return nil
}
