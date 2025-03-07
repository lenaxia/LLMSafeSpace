package kubernetes

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	
	"github.com/lenaxia/llmsafespace/api/internal/config"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Client manages Kubernetes API interactions
type Client struct {
	clientset       kubernetes.Interface
	dynamicClient   dynamic.Interface
	restConfig      *rest.Config
	informerFactory informers.SharedInformerFactory
	logger          *logger.Logger
	config          *config.Config
	stopCh          chan struct{}
}

// New creates a new Kubernetes client
func New(cfg *config.Config, logger *logger.Logger) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if cfg.Kubernetes.InCluster {
		// Use in-cluster config
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
		}
		logger.Info("Using in-cluster Kubernetes configuration")
	} else {
		// Use kubeconfig file
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.Kubernetes.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
		logger.Info("Using external Kubernetes configuration", "path", cfg.Kubernetes.ConfigPath)
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

	// Create dynamic client
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create informer factory with default resync period
	informerFactory := informers.NewSharedInformerFactory(clientset, time.Minute*30)

	return &Client{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		restConfig:      restConfig,
		informerFactory: informerFactory,
		logger:         logger,
		config:         cfg,
		stopCh:         make(chan struct{}),
	}, nil
}

// Start starts the informer factories and leader election
func (c *Client) Start() error {
	// Start informer factory
	c.informerFactory.Start(c.stopCh)
	c.logger.Info("Started informer factory")

	// Configure leader election if enabled
	if c.config.Kubernetes.LeaderElection.Enabled {
		go c.runLeaderElection()
	}

	return nil
}

// Stop stops the client and informer factories
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
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
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

// DynamicClient returns the dynamic client
func (c *Client) DynamicClient() dynamic.Interface {
	return c.dynamicClient
}

// RESTConfig returns the REST config
func (c *Client) RESTConfig() *rest.Config {
	return c.restConfig
}

// InformerFactory returns the informer factory
func (c *Client) InformerFactory() informers.SharedInformerFactory {
	return c.informerFactory
}

// LlmsafespaceV1 returns a client for the llmsafespace.dev/v1 API group
func (c *Client) LlmsafespaceV1() LLMSafespaceV1Interface {
	client, err := newLLMSafespaceV1Client(c.restConfig)
	if err != nil {
		c.logger.Error("Failed to create LLMSafespaceV1Client", err)
		return nil
	}
	return client
}
