package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/controller"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/metrics"
	"github.com/lenaxia/llmsafespace/src/sandbox-controller/internal/resources"
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
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Set up metrics
	metrics.SetupMetrics()

	// Create manager options
	options := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "llmsafespace-controller-leader-election",
	}

	// Configure leader election if enabled
	if enableLeaderElection {
		options.LeaderElectionConfig = &common.LeaderElectionConfig{
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
			Namespace:     "llmsafespace",
			Name:         "llmsafespace-controller",
		}
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
	
	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-warmpool", &webhook.Admission{
		Handler: &resources.WarmPoolValidator{Client: mgr.GetClient()},
	})
	
	mgr.GetWebhookServer().Register("/validate-llmsafespace-dev-v1-warmpod", &webhook.Admission{
		Handler: &resources.WarmPodValidator{Client: mgr.GetClient()},
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
