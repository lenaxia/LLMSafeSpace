package main

import (
	"flag"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/lenaxia/llmsafespace/controller/internal/controller"
	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
	"github.com/lenaxia/llmsafespace/controller/internal/resources"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = resources.AddToScheme(scheme)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var watchNamespaces string
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "",
		"Comma-separated list of namespaces the controller should watch. "+
			"Empty or '*' means watch all namespaces (default).")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Set up metrics
	metrics.SetupMetrics()

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

	// Register webhooks
	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-sandbox", &webhook.Admission{
		Handler: &resources.SandboxValidator{Client: mgr.GetClient()},
	})

	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-sandboxprofile", &webhook.Admission{
		Handler: &resources.SandboxProfileValidator{},
	})

	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-runtimeenvironment", &webhook.Admission{
		Handler: &resources.RuntimeEnvironmentValidator{},
	})

	// Set up controllers
	if err := controller.SetupControllers(mgr); err != nil {
		setupLog.Error(err, "unable to set up controllers")
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
