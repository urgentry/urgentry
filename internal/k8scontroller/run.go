package k8scontroller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

type RunConfig struct {
	Namespace        string
	MetricsAddress   string
	HealthAddress    string
	LeaderElection   bool
	LeaderElectionID string
}

func Run(ctx context.Context, cfg RunConfig) error {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add kubernetes scheme: %w", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add apps scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add core scheme: %w", err)
	}
	if err := AddToScheme(scheme); err != nil {
		return fmt.Errorf("add urgentry installation scheme: %w", err)
	}

	options := ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: cfg.HealthAddress,
		LeaderElection:         cfg.LeaderElection,
		LeaderElectionID:       cfg.LeaderElectionID,
	}
	if cfg.MetricsAddress != "" {
		options.Metrics.BindAddress = cfg.MetricsAddress
	}
	if cfg.Namespace != "" {
		options.Cache.DefaultNamespaces = map[string]cache.Config{
			cfg.Namespace: {},
		}
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		return fmt.Errorf("create controller manager: %w", err)
	}
	if err := (&InstallationReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("register installation controller: %w", err)
	}
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register healthz: %w", err)
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register readyz: %w", err)
	}
	return mgr.Start(ctx)
}
