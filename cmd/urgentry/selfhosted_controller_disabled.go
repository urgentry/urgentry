//go:build !k8scontroller

package main

import (
	"flag"

	"github.com/rs/zerolog/log"
)

func runSelfHostedController(args []string) {
	fs := flag.NewFlagSet("self-hosted controller", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted controller flags")
	}
	log.Fatal().Msg("self-hosted controller support is not built in; rebuild with URGENTRY_BUILD_TAGS including k8scontroller")
}

func selfHostedControllerUsage() string {
	return "Run the Kubernetes controller for UrgentryInstallation resources (requires build tag `k8scontroller`)"
}
