//go:build k8scontroller

package main

import (
	"flag"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	ctrl "sigs.k8s.io/controller-runtime"

	"urgentry/internal/k8scontroller"
)

func runSelfHostedController(args []string) {
	fs := flag.NewFlagSet("self-hosted controller", flag.ExitOnError)
	namespace := fs.String("namespace", strings.TrimSpace(os.Getenv("POD_NAMESPACE")), "namespace to watch; empty watches every namespace")
	metricsAddr := fs.String("metrics-bind-address", ":9090", "metrics bind address for the controller manager")
	healthAddr := fs.String("health-probe-bind-address", ":9091", "health probe bind address for the controller manager")
	leaderElect := fs.Bool("leader-elect", true, "enable leader election for the controller manager")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted controller flags")
	}
	if err := k8scontroller.Run(ctrl.SetupSignalHandler(), k8scontroller.RunConfig{
		Namespace:        *namespace,
		MetricsAddress:   *metricsAddr,
		HealthAddress:    *healthAddr,
		LeaderElection:   *leaderElect,
		LeaderElectionID: "urgentry-selfhosted-controller",
	}); err != nil {
		log.Fatal().Err(err).Msg("self-hosted controller failed")
	}
}

func selfHostedControllerUsage() string {
	return "Run the Kubernetes controller for UrgentryInstallation resources"
}
