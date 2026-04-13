package blob

import "fmt"

type Surface string

const (
	SurfaceAttachment Surface = "attachment"
	SurfaceReplay     Surface = "replay_asset"
	SurfaceProfile    Surface = "raw_profile"
	SurfaceDebug      Surface = "debug_artifact"
)

type ArchiveTier string

const (
	ArchiveTierHot  ArchiveTier = "hot"
	ArchiveTierWarm ArchiveTier = "warm"
	ArchiveTierCold ArchiveTier = "cold"
)

type LifecycleRule struct {
	Surface                Surface     `json:"surface"`
	PrimaryTier            ArchiveTier `json:"primaryTier"`
	ColdArchiveAllowed     bool        `json:"coldArchiveAllowed"`
	IntegrityProofRequired bool        `json:"integrityProofRequired"`
	RestoreSLO             string      `json:"restoreSLO"`
}

type LifecycleContract struct {
	Rules []LifecycleRule `json:"rules"`
}

func DefaultLifecycleContract() LifecycleContract {
	return LifecycleContract{
		Rules: []LifecycleRule{
			{
				Surface:                SurfaceAttachment,
				PrimaryTier:            ArchiveTierWarm,
				ColdArchiveAllowed:     true,
				IntegrityProofRequired: true,
				RestoreSLO:             "restore attachment blobs within one operator workflow and verify checksum before serving",
			},
			{
				Surface:                SurfaceReplay,
				PrimaryTier:            ArchiveTierWarm,
				ColdArchiveAllowed:     true,
				IntegrityProofRequired: true,
				RestoreSLO:             "restore replay assets before timeline playback reopens and verify manifest integrity",
			},
			{
				Surface:                SurfaceProfile,
				PrimaryTier:            ArchiveTierWarm,
				ColdArchiveAllowed:     true,
				IntegrityProofRequired: true,
				RestoreSLO:             "restore raw profile blobs before profile reconstruction resumes and verify payload hash",
			},
			{
				Surface:                SurfaceDebug,
				PrimaryTier:            ArchiveTierHot,
				ColdArchiveAllowed:     false,
				IntegrityProofRequired: true,
				RestoreSLO:             "keep debug artifacts hot until explicit archival support proves symbolication remains correct",
			},
		},
	}
}

func (c LifecycleContract) Validate() error {
	if len(c.Rules) != 4 {
		return fmt.Errorf("expected 4 blob lifecycle rules, got %d", len(c.Rules))
	}
	for _, rule := range c.Rules {
		if rule.Surface == "" || rule.PrimaryTier == "" || rule.RestoreSLO == "" {
			return fmt.Errorf("blob lifecycle rules must be complete")
		}
	}
	return nil
}
