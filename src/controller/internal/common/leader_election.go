package common

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
)

// LeaderElectionConfig contains configuration for leader election
type LeaderElectionConfig struct {
	// LeaseDuration is the duration that non-leader candidates will
	// wait to force acquire leadership
	LeaseDuration time.Duration
	// RenewDeadline is the duration that the acting master will retry
	// refreshing leadership before giving up
	RenewDeadline time.Duration
	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions
	RetryPeriod time.Duration
	// Namespace is the namespace where the lock resource exists
	Namespace string
	// Name is the name of the lock resource
	Name string
}

// SetupLeaderElection configures and starts leader election
func SetupLeaderElection(cfg *LeaderElectionConfig, kubeClient kubernetes.Interface, runFunc func(context.Context)) error {
	if cfg == nil {
		cfg = &LeaderElectionConfig{
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
			Namespace:     "llmsafespace",
			Name:         "llmsafespace-controller",
		}
	}

	// Get hostname for leader identity
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %v", err)
	}
	id := hostname + "_" + uuid.NewString()

	// Create a new resource lock
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Client: kubeClient.CoordinationV1(),
	}

	// Start leader election
	leaderelection.RunOrDie(context.Background(), leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				klog.Infof("Started leading as %s", id)
				runFunc(ctx)
			},
			OnStoppedLeading: func() {
				klog.Infof("Leader election lost for %s", id)
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				if identity != id {
					klog.Infof("New leader elected: %s", identity)
				}
			},
		},
	})

	return nil
}
