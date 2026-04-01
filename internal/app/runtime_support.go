package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
	"urgentry/internal/config"
	"urgentry/internal/nativesym"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func openBlobStore(cfg config.Config, dataDir string) (store.BlobStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.BlobBackend)) {
	case "", "file":
		return store.NewFileBlobStore(filepath.Join(dataDir, "blobs"))
	case "s3":
		if strings.TrimSpace(cfg.S3Endpoint) == "" {
			return nil, fmt.Errorf("s3 endpoint is required")
		}
		if strings.TrimSpace(cfg.S3Bucket) == "" {
			return nil, fmt.Errorf("s3 bucket is required")
		}
		return store.NewS3BlobStore(store.S3BlobConfig{
			Endpoint:  cfg.S3Endpoint,
			Bucket:    cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey,
			SecretKey: cfg.S3SecretKey,
			Region:    cfg.S3Region,
			Prefix:    cfg.S3Prefix,
			UseTLS:    cfg.S3UseTLS,
		})
	default:
		return nil, fmt.Errorf("unsupported blob backend %q", cfg.BlobBackend)
	}
}

type nativeDebugFileStore struct {
	store *sqlite.DebugFileStore
}

func (s *nativeDebugFileStore) LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*nativesym.File, []byte, error) {
	item, body, err := s.store.LookupByDebugID(ctx, projectID, releaseVersion, kind, debugID)
	if err != nil || item == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: item.ID, CodeID: item.CodeID, Kind: item.Kind}, body, nil
}

func (s *nativeDebugFileStore) LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*nativesym.File, []byte, error) {
	item, body, err := s.store.LookupByCodeID(ctx, projectID, releaseVersion, kind, codeID)
	if err != nil || item == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: item.ID, CodeID: item.CodeID, Kind: item.Kind}, body, nil
}

// ensureDefaultAlertRule creates a "First Seen" alert rule if no rules exist.
func ensureDefaultAlertRule(store alert.RuleStore) {
	ctx := context.Background()
	rules, err := store.ListRules(ctx, "__default__")
	if err != nil {
		log.Warn().Err(err).Msg("could not check for default alert rules")
		return
	}
	if len(rules) > 0 {
		return
	}

	now := time.Now().UTC()
	rule := &alert.Rule{
		ProjectID: "__default__",
		Name:      "First Seen",
		RuleType:  "any",
		Status:    "active",
		Conditions: []alert.Condition{
			{
				ID:   "sentry.rules.conditions.first_seen_event.FirstSeenEventCondition",
				Name: "A new issue is created",
			},
		},
		Actions:   []alert.Action{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateRule(ctx, rule); err != nil {
		log.Warn().Err(err).Msg("could not create default alert rule")
		return
	}
	log.Info().Str("rule_id", rule.ID).Msg("created default 'First Seen' alert rule")
}
