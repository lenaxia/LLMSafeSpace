// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/lenaxia/llmsafespace/controller/internal/controller"
	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
	"github.com/lenaxia/llmsafespace/controller/internal/webhooks"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// ldflags injection targets — set by the build system.
	version   = "dev"
	commitSHA = "unknown"
	buildTime = "unknown"
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var watchNamespaces string
	var allowedImageRegistries string
	var allowedStorageClassNames string
	var maxStorageGi int64
	var maxCPUMillicores int64
	var maxMemoryMi int64
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "",
		"Comma-separated list of namespaces the controller should watch. "+
			"Empty or '*' means watch all namespaces (default).")
	flag.StringVar(&allowedImageRegistries, "allowed-image-registries", "",
		"Comma-separated list of registry prefixes accepted as Workspace.spec.runtime "+
			"image references (e.g. 'ghcr.io/lenaxia/,registry.k8s.io/'). Empty list "+
			"means only RuntimeEnvironment-name references are allowed (G2 / F1.2.1).")
	flag.StringVar(&allowedStorageClassNames, "allowed-storage-class-names", "",
		"Comma-separated list of StorageClass names accepted as "+
			"Workspace.spec.storage.storageClassName. Empty means accept any (G2 / F1.2.9).")
	flag.Int64Var(&maxStorageGi, "max-workspace-storage-gi", 1024,
		"Maximum spec.storage.size in GiB. Set 0 to disable. (G2 / RT-6.1).")
	flag.Int64Var(&maxCPUMillicores, "max-workspace-cpu-millicores", 16000,
		"Maximum spec.resources.cpu in millicores (16000 = 16 cores). Set 0 to disable. (G4 / F1.2.3).")
	flag.Int64Var(&maxMemoryMi, "max-workspace-memory-mi", 65536,
		"Maximum spec.resources.memory in MiB (65536 = 64GiB). Set 0 to disable. (G4 / F1.2.3).")
	var inferenceRelayURL string
	flag.StringVar(&inferenceRelayURL, "inference-relay-url", "",
		"Cloudflare Worker URL for free-tier inference relay (Epic 26). "+
			"When set, workspace pods route opencode requests through this URL for IP distribution.")
	var inferenceRelaySecret string
	flag.StringVar(&inferenceRelaySecret, "inference-relay-secret", "",
		"Path-segment secret for the inference relay Worker (Epic 26). "+
			"Appended to --inference-relay-url as the first path segment; the Worker validates and strips it.")
	var enableRelayController bool
	flag.BoolVar(&enableRelayController, "enable-inference-relay", false,
		"Enable the InferenceRelay controller (Epic 42). When true, the controller reconciles InferenceRelay CRs and manages relay VMs.")
	var relayRouterURL string
	flag.StringVar(&relayRouterURL, "relay-router-url", "http://relay-router:8080",
		"URL of the in-cluster relay-router for /metrics scraping (Epic 42).")
	var apiServiceURL string
	flag.StringVar(&apiServiceURL, "api-service-url", "",
		"Root URL of the in-cluster API service (e.g. http://llmsafespace-api.llmsafespace.svc:8080) "+
			"used to poll org status for D20 org-level workspace suspension. "+
			"Empty disables org-suspension (the controller never org-suspends).")
	flag.Parse()

	// US-43.19 / D20: the shared secret authenticating controller→API internal
	// calls. Read from the same env var the API service uses
	// (LLMSAFESPACE_INTERNAL_TOKEN) so a single mounted Secret configures both
	// sides. Empty means no X-Internal-Token header is sent (the endpoint is
	// then reachable only when the API side also has the env unset).
	apiInternalToken := os.Getenv("LLMSAFESPACE_INTERNAL_TOKEN")

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	setupLog.Info("starting controller", "version", version, "commit", commitSHA, "built", buildTime)

	// Register custom metrics with the controller-runtime metrics registry
	// (not prometheus.DefaultRegisterer). controller-runtime v0.15+ serves
	// /metrics from its own private registry; registering on the global
	// default makes the metrics invisible to the scrape endpoint.
	if err := metrics.RegisterWith(ctrlmetrics.Registry); err != nil {
		setupLog.Error(err, "unable to register custom metrics")
		os.Exit(1)
	}

	// Create manager options
	options := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "llmsafespace-controller-leader-election",
	}

	// Restrict the cache (and thus the controllers) to a specific set of
	// namespaces if --watch-namespaces is set. Empty or "*" means cluster-wide.
	if nsMap := parseWatchNamespaces(watchNamespaces); nsMap != nil {
		options.Cache = cache.Options{DefaultNamespaces: nsMap}
		setupLog.Info("watching specific namespaces", "namespaces", watchNamespaces)
	} else {
		setupLog.Info("watching all namespaces")
	}

	if enableLeaderElection {
		leaseDuration := 15 * time.Second
		renewDeadline := 10 * time.Second
		retryPeriod := 2 * time.Second
		options.LeaseDuration = &leaseDuration
		options.RenewDeadline = &renewDeadline
		options.RetryPeriod = &retryPeriod
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Register webhooks. We construct the decoder explicitly because
	// controller-runtime v0.15+ removed the InjectDecoder dependency-injection
	// pattern; webhooks now require their decoder to be set at construction.
	// Without this, all admission requests panic with nil-pointer-deref on
	// the nil decoder.
	webhookDecoder := admission.NewDecoder(mgr.GetScheme())

	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-runtimeenvironment", &webhook.Admission{
		Handler: &webhooks.RuntimeEnvironmentValidator{
			Decoder:                webhookDecoder,
			AllowedImageRegistries: splitNonEmpty(allowedImageRegistries, ","),
		},
	})

	// G2 — Workspace admission webhook closes F1.2.1 (registry allow-list),
	// F1.2.2 (status forge), F1.2.9 (storage class allow-list), and RT-6.1
	// (storage size upper bound). Configuration is operator-supplied via
	// flags so the same chart works in every deployment topology.
	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-workspace", &webhook.Admission{
		Handler: &webhooks.WorkspaceValidator{
			Decoder:                  webhookDecoder,
			AllowedImageRegistries:   splitNonEmpty(allowedImageRegistries, ","),
			AllowedStorageClassNames: splitNonEmpty(allowedStorageClassNames, ","),
			MaxStorageGi:             maxStorageGi,
			MaxCPUMillicores:         maxCPUMillicores,
			MaxMemoryMi:              maxMemoryMi,
		},
	})

	// Set up controllers
	if err := controller.SetupControllers(mgr, inferenceRelayURL, inferenceRelaySecret, apiServiceURL, apiInternalToken); err != nil {
		setupLog.Error(err, "unable to set up controllers")
		os.Exit(1)
	}

	// Set up InferenceRelay controller (feature-gated)
	relayNamespace := os.Getenv("POD_NAMESPACE")
	if relayNamespace == "" {
		relayNamespace = "llmsafespace"
	}
	if err := controller.SetupRelayController(mgr, relayNamespace, relayRouterURL, enableRelayController); err != nil {
		setupLog.Error(err, "unable to set up InferenceRelay controller")
		os.Exit(1)
	}

	// Seed the WorkspacesRunning gauge from current cluster state.
	// Without this, the gauge drifts negative on controller restart
	// because existing Active workspaces don't re-trigger the
	// Creating→Active transition that calls .Inc(), but Suspend
	// unconditionally calls .Dec().
	if err := mgr.Add(&workspaceGaugeSeeder{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to add workspace gauge seeder")
		os.Exit(1)
	}

	// Add health check endpoints
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type workspaceGaugeSeeder struct {
	client.Client
}

func (s *workspaceGaugeSeeder) Start(ctx context.Context) error {
	logger := ctrl.Log.WithName("workspace-gauge-seeder")
	wsList := &v1.WorkspaceList{}
	if err := s.List(ctx, wsList); err != nil {
		return fmt.Errorf("seed workspaces running gauge: %w", err)
	}
	counts := map[[2]string]int{}
	inRecovery := 0
	inSafeMode := 0
	for _, ws := range wsList.Items {
		if ws.Status.Phase == v1.WorkspacePhaseActive {
			runtime := ws.Spec.Runtime
			secLevel := string(ws.Spec.SecurityLevel)
			counts[[2]string{runtime, secLevel}]++
		}
		// US-24.11: seed recovery + safe-mode gauges so they survive controller restart.
		if ws.Status.ConsecutiveFailures > 0 && ws.Status.Phase != v1.WorkspacePhaseActive {
			inRecovery++
		}
		if ws.Status.SafeMode {
			inSafeMode++
		}
	}
	for k, n := range counts {
		metrics.SeedWorkspacesRunning(k[0], k[1], n)
		logger.Info("seeded WorkspacesRunning gauge", "runtime", k[0], "security_level", k[1], "count", n)
	}
	metrics.WorkspacesInRecovery.Set(float64(inRecovery))
	metrics.WorkspaceSafeModeActive.Set(float64(inSafeMode))
	if inRecovery > 0 || inSafeMode > 0 {
		logger.Info("seeded recovery gauges", "in_recovery", inRecovery, "in_safe_mode", inSafeMode)
	}
	return nil
}
