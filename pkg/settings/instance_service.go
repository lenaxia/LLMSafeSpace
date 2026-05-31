// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// InstanceStore is the database interface for instance settings.
type InstanceStore interface {
	GetAllInstanceSettings(ctx context.Context) (map[string]json.RawMessage, error)
	SetInstanceSetting(ctx context.Context, key string, value json.RawMessage) error
}

// InstanceService implements InstanceSettingsService with a full-map cache
// and singleflight to prevent thundering herd on TTL expiry.
type InstanceService struct {
	store  InstanceStore
	logger pkginterfaces.LoggerInterface

	mu       sync.RWMutex
	data     map[string]any // decoded values
	loadedAt time.Time
	ttl      time.Duration
	sf       singleflight.Group

	index map[string]SettingDef
}

// NewInstanceService creates a new instance settings service.
func NewInstanceService(store InstanceStore, logger pkginterfaces.LoggerInterface) *InstanceService {
	return &InstanceService{
		store:  store,
		logger: logger,
		ttl:    60 * time.Second,
		index:  InstanceSettingIndex(),
	}
}

func (s *InstanceService) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.loadFromDB(ctx)
	return err
}

func (s *InstanceService) Stop() error { return nil }

// Schema returns the Tier 2 setting definitions.
func (s *InstanceService) Schema() []SettingDef {
	return InstanceSettings()
}

// GetBool returns a bool setting value.
func (s *InstanceService) GetBool(ctx context.Context, key string) (bool, error) {
	v, err := s.get(ctx, key)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("key %q: expected bool, got %T", key, v)
	}
	return b, nil
}

// GetInt returns an int setting value.
func (s *InstanceService) GetInt(ctx context.Context, key string) (int, error) {
	v, err := s.get(ctx, key)
	if err != nil {
		return 0, err
	}
	n, ok := toInt(v)
	if !ok {
		return 0, fmt.Errorf("key %q: expected int, got %T", key, v)
	}
	return n, nil
}

// GetString returns a string setting value.
func (s *InstanceService) GetString(ctx context.Context, key string) (string, error) {
	v, err := s.get(ctx, key)
	if err != nil {
		return "", err
	}
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("key %q: expected string, got %T", key, v)
	}
	return str, nil
}

// GetStrings returns a []string setting value.
func (s *InstanceService) GetStrings(ctx context.Context, key string) ([]string, error) {
	v, err := s.get(ctx, key)
	if err != nil {
		return nil, err
	}
	switch val := v.(type) {
	case []string:
		return val, nil
	case []any:
		result := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("key %q: element %d is %T, expected string", key, i, item)
			}
			result[i] = s
		}
		return result, nil
	default:
		return nil, fmt.Errorf("key %q: expected []string, got %T", key, v)
	}
}

// GetAll returns all instance settings merged with schema defaults.
func (s *InstanceService) GetAll(ctx context.Context) (map[string]any, error) {
	data, err := s.ensureCache(ctx)
	if err != nil {
		return nil, err
	}
	// Merge with defaults
	result := make(map[string]any, len(s.index))
	for key, def := range s.index {
		result[key] = def.Default
	}
	for key, val := range data {
		result[key] = val
	}
	return result, nil
}

// Set validates and persists a setting value, then invalidates the cache.
func (s *InstanceService) Set(ctx context.Context, key string, value any) error {
	def, ok := s.index[key]
	if !ok {
		return fmt.Errorf("unknown instance setting key: %q", key)
	}
	if err := Validate(def, value); err != nil {
		return err
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value for key %q: %w", key, err)
	}

	if err := s.store.SetInstanceSetting(ctx, key, raw); err != nil {
		return fmt.Errorf("failed to persist key %q: %w", key, err)
	}

	// Invalidate cache and reload
	s.mu.Lock()
	s.data = nil
	s.loadedAt = time.Time{}
	s.mu.Unlock()

	// Emit audit log
	if s.logger != nil {
		s.logger.Info("instance_setting_changed",
			"key", key,
			"new_value", string(raw),
		)
	}

	return nil
}

// get retrieves a single setting value, using cache with schema default fallback.
func (s *InstanceService) get(ctx context.Context, key string) (any, error) {
	def, ok := s.index[key]
	if !ok {
		return nil, fmt.Errorf("unknown instance setting key: %q", key)
	}

	data, err := s.ensureCache(ctx)
	if err != nil {
		return nil, err
	}

	if val, exists := data[key]; exists {
		return val, nil
	}
	return def.Default, nil
}

// ensureCache returns the cached data, refreshing if stale or empty.
func (s *InstanceService) ensureCache(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	if s.data != nil && time.Since(s.loadedAt) < s.ttl {
		data := s.data
		s.mu.RUnlock()
		return data, nil
	}
	s.mu.RUnlock()

	// Use singleflight to prevent thundering herd
	result, err, _ := s.sf.Do("load", func() (any, error) {
		return s.loadFromDB(ctx)
	})
	if err != nil {
		return nil, err
	}
	return result.(map[string]any), nil
}

// loadFromDB fetches all instance settings from the database and caches them.
func (s *InstanceService) loadFromDB(ctx context.Context) (map[string]any, error) {
	rawMap, err := s.store.GetAllInstanceSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load instance settings: %w", err)
	}

	data := make(map[string]any, len(rawMap))
	for key, raw := range rawMap {
		var val any
		if err := json.Unmarshal(raw, &val); err != nil {
			if s.logger != nil {
				s.logger.Warn("failed to unmarshal instance setting",
					"key", key,
					"error", err.Error(),
				)
			}
			continue
		}
		data[key] = val
	}

	s.mu.Lock()
	s.data = data
	s.loadedAt = time.Now()
	s.mu.Unlock()

	return data, nil
}
