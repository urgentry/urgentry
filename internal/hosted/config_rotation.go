package hosted

import (
	"fmt"
	"slices"
)

type ConfigScope string

const (
	ConfigScopeEnvironment ConfigScope = "environment"
	ConfigScopeRegion      ConfigScope = "region"
	ConfigScopeCell        ConfigScope = "cell"
)

var configScopeOrder = []ConfigScope{
	ConfigScopeEnvironment,
	ConfigScopeRegion,
	ConfigScopeCell,
}

type ConfigKeyspace string

const (
	ConfigKeyspaceRuntime      ConfigKeyspace = "runtime"
	ConfigKeyspaceIngest       ConfigKeyspace = "ingest"
	ConfigKeyspaceQuota        ConfigKeyspace = "quota"
	ConfigKeyspaceAuth         ConfigKeyspace = "auth"
	ConfigKeyspaceAlerting     ConfigKeyspace = "alerting"
	ConfigKeyspaceBilling      ConfigKeyspace = "billing"
	ConfigKeyspaceFeatureFlags ConfigKeyspace = "feature_flags"
)

var configKeyspaceOrder = []ConfigKeyspace{
	ConfigKeyspaceRuntime,
	ConfigKeyspaceIngest,
	ConfigKeyspaceQuota,
	ConfigKeyspaceAuth,
	ConfigKeyspaceAlerting,
	ConfigKeyspaceBilling,
	ConfigKeyspaceFeatureFlags,
}

type SecretKind string

const (
	SecretKindSessionSigning SecretKind = "session_signing"
	SecretKindBootstrap      SecretKind = "bootstrap"
	SecretKindSMTP           SecretKind = "smtp"
	SecretKindS3             SecretKind = "s3"
	SecretKindOAuth          SecretKind = "oauth"
	SecretKindWebhook        SecretKind = "webhook"
	SecretKindMetrics        SecretKind = "metrics"
)

var secretKindOrder = []SecretKind{
	SecretKindSessionSigning,
	SecretKindBootstrap,
	SecretKindSMTP,
	SecretKindS3,
	SecretKindOAuth,
	SecretKindWebhook,
	SecretKindMetrics,
}

type RotationStep string

const (
	RotationStepPrepareNext RotationStep = "prepare_next"
	RotationStepPublishDual RotationStep = "publish_dual_read"
	RotationStepVerifyNext  RotationStep = "verify_next"
	RotationStepPromoteNext RotationStep = "promote_next"
	RotationStepRevokePrior RotationStep = "revoke_prior"
)

var rotationStepOrder = []RotationStep{
	RotationStepPrepareNext,
	RotationStepPublishDual,
	RotationStepVerifyNext,
	RotationStepPromoteNext,
	RotationStepRevokePrior,
}

type ConfigLayer struct {
	Scope          ConfigScope `json:"scope"`
	AllowOverrides bool        `json:"allowOverrides"`
}

type SecretRotationPolicy struct {
	MaxRotationHours   int            `json:"maxRotationHours"`
	DualReadKinds      []SecretKind   `json:"dualReadKinds"`
	RestartRequired    []SecretKind   `json:"restartRequiredKinds"`
	RequiredAuditNotes []string       `json:"requiredAuditNotes"`
	Steps              []RotationStep `json:"steps"`
}

type RegionConfigPolicy struct {
	Layers          []ConfigLayer        `json:"layers"`
	Keyspaces       []ConfigKeyspace     `json:"keyspaces"`
	RequiredSecrets []SecretKind         `json:"requiredSecrets"`
	SecretRotation  SecretRotationPolicy `json:"secretRotation"`
}

func DefaultRegionConfigPolicy() RegionConfigPolicy {
	return RegionConfigPolicy{
		Layers: []ConfigLayer{
			{Scope: ConfigScopeEnvironment, AllowOverrides: true},
			{Scope: ConfigScopeRegion, AllowOverrides: true},
			{Scope: ConfigScopeCell, AllowOverrides: true},
		},
		Keyspaces:       append([]ConfigKeyspace(nil), configKeyspaceOrder...),
		RequiredSecrets: append([]SecretKind(nil), secretKindOrder...),
		SecretRotation: SecretRotationPolicy{
			MaxRotationHours: 24,
			DualReadKinds: []SecretKind{
				SecretKindSessionSigning,
				SecretKindOAuth,
				SecretKindWebhook,
			},
			RestartRequired: []SecretKind{
				SecretKindBootstrap,
				SecretKindMetrics,
			},
			RequiredAuditNotes: []string{
				"old bundle version",
				"new bundle version",
				"rotation scope",
				"approver id",
				"rollback bundle id",
			},
			Steps: append([]RotationStep(nil), rotationStepOrder...),
		},
	}
}

func (p RegionConfigPolicy) Validate() error {
	if len(p.Layers) != len(configScopeOrder) {
		return fmt.Errorf("expected %d config layers, got %d", len(configScopeOrder), len(p.Layers))
	}
	for i, scope := range configScopeOrder {
		layer := p.Layers[i]
		if layer.Scope != scope {
			return fmt.Errorf("layer %d scope = %q, want %q", i, layer.Scope, scope)
		}
	}
	if len(p.Keyspaces) != len(configKeyspaceOrder) {
		return fmt.Errorf("expected %d keyspaces, got %d", len(configKeyspaceOrder), len(p.Keyspaces))
	}
	for _, keyspace := range configKeyspaceOrder {
		if !slices.Contains(p.Keyspaces, keyspace) {
			return fmt.Errorf("missing config keyspace %q", keyspace)
		}
	}
	if len(p.RequiredSecrets) != len(secretKindOrder) {
		return fmt.Errorf("expected %d required secrets, got %d", len(secretKindOrder), len(p.RequiredSecrets))
	}
	for _, kind := range secretKindOrder {
		if !slices.Contains(p.RequiredSecrets, kind) {
			return fmt.Errorf("missing secret kind %q", kind)
		}
	}
	if p.SecretRotation.MaxRotationHours <= 0 {
		return fmt.Errorf("max rotation hours must be positive")
	}
	if len(p.SecretRotation.RequiredAuditNotes) == 0 {
		return fmt.Errorf("required audit notes must not be empty")
	}
	if len(p.SecretRotation.Steps) != len(rotationStepOrder) {
		return fmt.Errorf("expected %d rotation steps, got %d", len(rotationStepOrder), len(p.SecretRotation.Steps))
	}
	for i, step := range rotationStepOrder {
		if p.SecretRotation.Steps[i] != step {
			return fmt.Errorf("rotation step %d = %q, want %q", i, p.SecretRotation.Steps[i], step)
		}
	}
	for _, kind := range p.SecretRotation.DualReadKinds {
		if !slices.Contains(secretKindOrder, kind) {
			return fmt.Errorf("unknown dual-read secret kind %q", kind)
		}
	}
	for _, kind := range p.SecretRotation.RestartRequired {
		if !slices.Contains(secretKindOrder, kind) {
			return fmt.Errorf("unknown restart-required secret kind %q", kind)
		}
	}
	return nil
}
