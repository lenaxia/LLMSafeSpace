// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package kubernetes

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/lenaxia/llmsafespaces/pkg/config"
	"github.com/lenaxia/llmsafespaces/pkg/interfaces"
	pkglogger "github.com/lenaxia/llmsafespaces/pkg/logger"
)

// Client manages Kubernetes API interactions
type Client struct {
	clientset       kubernetes.Interface
	dynamicClient   dynamic.Interface
	restConfig      *rest.Config
	informerFactory informers.SharedInformerFactory
	logger          interfaces.LoggerInterface
	config          *config.KubernetesConfig
	stopCh          chan struct{}

	v1Once   sync.Once
	v1Client *LLMSafespacesV1Client
	v1Err    error
}

// Ensure Client implements interfaces.KubernetesClient
var _ interfaces.KubernetesClient = (*Client)(nil)

// New creates a new Kubernetes client
func New(cfg *config.KubernetesConfig, log interfaces.LoggerInterface) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if cfg.InCluster {
		// Use in-cluster config
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
		}
		log.Info("Using in-cluster Kubernetes configuration")
	} else {
		// Use kubeconfig file
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
		log.Info("Using external Kubernetes configuration", "path", cfg.ConfigPath)
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
		logger:          log,
		config:          cfg,
		stopCh:          make(chan struct{}),
	}, nil
}

// NewForTesting creates a new Kubernetes client for testing
func NewForTesting(
	clientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	restConfig *rest.Config,
	informerFactory informers.SharedInformerFactory,
	log interfaces.LoggerInterface,
) *Client {
	if log == nil {
		loggerImpl, err := pkglogger.New(true, "debug", "console")
		if err != nil {
			defaultLogger, _ := pkglogger.New(true, "debug", "console")
			log = defaultLogger
		} else {
			log = loggerImpl
		}
	}

	return &Client{
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		restConfig:      restConfig,
		informerFactory: informerFactory,
		logger:          log,
		config:          &config.KubernetesConfig{},
		stopCh:          make(chan struct{}),
	}
}

// Start starts the informer factories and leader election
func (c *Client) Start() error {
	if c.informerFactory != nil {
		c.informerFactory.Start(c.stopCh)
		c.logger.Info("Started informer factory")
	}

	if c.config.LeaderElection.Enabled {
		if err := c.runLeaderElection(context.Background()); err != nil {
			return fmt.Errorf("failed to start leader election: %w", err)
		}
	}

	return nil
}

// Stop stops the client and informer factories
func (c *Client) Stop() {
	close(c.stopCh)
	c.logger.Info("Stopped Kubernetes client")
}

// runLeaderElection starts the leader election process
func (c *Client) runLeaderElection(ctx context.Context) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "llmsafespaces-api-leader",
			Namespace: c.config.Namespace,
		},
		Client: c.clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: c.config.PodName,
		},
	}

	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   c.config.LeaderElection.LeaseDuration,
		RenewDeadline:   c.config.LeaderElection.RenewDeadline,
		RetryPeriod:     c.config.LeaderElection.RetryPeriod,
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
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	go elector.Run(ctx)
	return nil
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

// LlmsafespacesV1 returns a client for the llmsafespaces.dev/v1 API group.
//
// Constructs a typed REST client by deriving a copy of the base rest.Config
// with the LLMSafeSpaces GroupVersion, /apis path, and a serializer from the
// scheme that has our types registered (via the init() in client_crds.go).
//
// Without these ContentConfig fields rest.RESTClientFor returns an error
// (or returns a partially-initialized client that nil-panics on Watch),
// which silently breaks the WorkspaceWatcher and the proxy ownership middleware.
func (c *Client) LlmsafespacesV1() (interfaces.LLMSafespacesV1Interface, error) {
	c.v1Once.Do(func() {
		client, err := newLLMSafespacesV1Client(c.restConfig)
		if err != nil {
			c.logger.Error("failed to construct LlmsafespacesV1 REST client", err)
			c.v1Err = err
			return
		}
		c.v1Client = client
	})
	if c.v1Err != nil {
		return nil, c.v1Err
	}
	return c.v1Client, nil
}
