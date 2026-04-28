package app

import (
	"github.com/rs/zerolog/log"

	"urgentry/internal/config"
	"urgentry/internal/requestmeta"
)

/*
App runtime flow

	Run
	  -> newRuntimeState
	     -> open DBs / backends
	     -> build stores / pipeline / query services
	  -> runtime.run
	     -> boot API state
	     -> start worker / scheduler
	     -> serve HTTP
	     -> shutdown / close runtime
*/

// runOptions holds optional config for Run.
type runOptions struct {
	version string
}

// RunOption configures Run.
type RunOption func(*runOptions)

// WithVersion sets the build version for healthz and metrics.
func WithVersion(v string) RunOption {
	return func(o *runOptions) { o.version = v }
}

func Run(cfg config.Config, role Role, opts ...RunOption) error {
	if err := requestmeta.ConfigureTrustedProxies(cfg.TrustedProxyCIDRs); err != nil {
		return err
	}
	var runOpts runOptions
	for _, o := range opts {
		o(&runOpts)
	}

	log.Info().Str("role", string(role)).Str("addr", cfg.HTTPAddr).Msg("starting urgentry")

	runtime, err := newRuntimeState(cfg, role, runOpts.version)
	if err != nil {
		return err
	}
	defer runtime.close()

	return runtime.run()
}
