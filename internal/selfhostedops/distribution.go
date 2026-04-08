package selfhostedops

import "fmt"

type DistributionKind string

const (
	DistributionCompose    DistributionKind = "compose"
	DistributionKubernetes DistributionKind = "kubernetes"
	DistributionHelm       DistributionKind = "helm"
)

type SecretSource string

const (
	SecretSourceEnvFile         SecretSource = "env_file"
	SecretSourceSecretManifest  SecretSource = "secret_manifest"
	SecretSourceExternalSecrets SecretSource = "external_secrets"
)

type DistributionBundle struct {
	Kind              DistributionKind `json:"kind"`
	InstallGuide      string           `json:"installGuide"`
	SecretSource      SecretSource     `json:"secretSource"`
	SmokeCommand      string           `json:"smokeCommand"`
	UpgradeCommand    string           `json:"upgradeCommand"`
	RequiredArtifacts []string         `json:"requiredArtifacts"`
}

type DistributionContract struct {
	Bundles []DistributionBundle `json:"bundles"`
}

func DefaultDistributionContract() DistributionContract {
	return DistributionContract{
		Bundles: []DistributionBundle{
			{
				Kind:           DistributionCompose,
				InstallGuide:   "docs/urgentry-serious-self-hosted-deployment-guide.md",
				SecretSource:   SecretSourceEnvFile,
				SmokeCommand:   "deploy/compose/smoke.sh",
				UpgradeCommand: "deploy/compose/upgrade.sh",
				RequiredArtifacts: []string{
					"deploy/compose/compose.yaml",
					"deploy/compose/example.env",
					"deploy/compose/smoke.sh",
				},
			},
			{
				Kind:           DistributionKubernetes,
				InstallGuide:   "docs/urgentry-serious-self-hosted-kubernetes-and-helm.md",
				SecretSource:   SecretSourceSecretManifest,
				SmokeCommand:   "deploy/k8s/smoke.sh",
				UpgradeCommand: "kubectl apply -f deploy/k8s/",
				RequiredArtifacts: []string{
					"deploy/k8s/configmap.yaml",
					"deploy/k8s/secret.yaml",
					"deploy/k8s/smoke.sh",
				},
			},
			{
				Kind:           DistributionHelm,
				InstallGuide:   "docs/urgentry-serious-self-hosted-kubernetes-and-helm.md",
				SecretSource:   SecretSourceExternalSecrets,
				SmokeCommand:   "deploy/k8s/smoke.sh",
				UpgradeCommand: "helm upgrade --install urgentry deploy/helm",
				RequiredArtifacts: []string{
					"deploy/helm/Chart.yaml",
					"deploy/helm/values.yaml",
				},
			},
		},
	}
}

func (c DistributionContract) Validate() error {
	if len(c.Bundles) != 3 {
		return fmt.Errorf("expected 3 distribution bundles, got %d", len(c.Bundles))
	}
	seen := map[DistributionKind]struct{}{}
	for _, item := range c.Bundles {
		if item.Kind == "" {
			return fmt.Errorf("distribution kind is required")
		}
		if _, ok := seen[item.Kind]; ok {
			return fmt.Errorf("duplicate distribution kind %q", item.Kind)
		}
		seen[item.Kind] = struct{}{}
		if item.InstallGuide == "" || item.SmokeCommand == "" || item.UpgradeCommand == "" {
			return fmt.Errorf("distribution %q is incomplete", item.Kind)
		}
		if len(item.RequiredArtifacts) == 0 {
			return fmt.Errorf("distribution %q must define required artifacts", item.Kind)
		}
	}
	return nil
}
