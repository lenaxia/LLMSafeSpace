// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"fmt"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// SeedStore is the database interface needed by the seed job.
type SeedStore interface {
	GetAllInstanceSettings(ctx context.Context) (map[string]json.RawMessage, error)
	InsertInstanceSettingIfMissing(ctx context.Context, key string, value json.RawMessage) (inserted bool, err error)
}

// SeedResult contains the outcome of a seed operation.
type SeedResult struct {
	Inserted int
	Skipped  int
	Orphaned []string
}

// Seed inserts schema defaults for any missing instance settings keys and
// detects orphaned keys (in DB but not in current schema).
func Seed(ctx context.Context, store SeedStore, logger pkginterfaces.LoggerInterface) (*SeedResult, error) {
	result := &SeedResult{}

	// Get current DB state
	existing, err := store.GetAllInstanceSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("seed: failed to read existing settings: %w", err)
	}

	// Build index of current schema keys
	schemaKeys := make(map[string]bool)
	for _, def := range InstanceSettings() {
		schemaKeys[def.Key] = true

		raw, err := json.Marshal(def.Default)
		if err != nil {
			return nil, fmt.Errorf("seed: failed to marshal default for %q: %w", def.Key, err)
		}

		inserted, err := store.InsertInstanceSettingIfMissing(ctx, def.Key, raw)
		if err != nil {
			return nil, fmt.Errorf("seed: failed to insert %q: %w", def.Key, err)
		}

		if inserted {
			result.Inserted++
		} else {
			result.Skipped++
			if logger != nil {
				logger.Info("instance_setting_seed_skipped",
					"key", def.Key,
					"schema_default", string(raw),
				)
			}
		}
	}

	// Detect orphaned keys
	for key := range existing {
		if !schemaKeys[key] {
			result.Orphaned = append(result.Orphaned, key)
			if logger != nil {
				logger.Warn("instance_setting_orphaned",
					"key", key,
					"hint", fmt.Sprintf("removed in schema v%d, consider DELETE", SchemaVersion),
				)
			}
		}
	}

	return result, nil
}
