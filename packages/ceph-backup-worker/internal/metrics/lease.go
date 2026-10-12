package metrics

import (
	"context"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// LeaseConfig names the Lease (coordination.k8s.io) the pods elect the
// metrics leader with, and this pod's identity in it.
type LeaseConfig struct {
	Namespace string
	Name      string
	Identity  string
}

// RunElected runs lead whenever this pod holds the Lease, with a context
// that ends when it loses it, until ctx ends. It uses the pod's service
// account.
func RunElected(ctx context.Context, lc LeaseConfig, lead func(context.Context)) error {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Namespace: lc.Namespace, Name: lc.Name},
		Client:     cs.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: lc.Identity},
	}
	for ctx.Err() == nil {
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   60 * time.Second,
			RenewDeadline:   40 * time.Second,
			RetryPeriod:     10 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					slog.Info("holding the metrics lease", "lease", lc.Name)
					lead(ctx)
				},
				OnStoppedLeading: func() { slog.Info("lost the metrics lease", "lease", lc.Name) },
			},
		})
	}
	return nil
}
