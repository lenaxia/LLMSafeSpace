// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/json"
	"fmt"

	pkginterfaces "github.com/lenaxia/llmsafespaces/pkg/interfaces"
)

// UserStore is the database interface for user settings.
type UserStore interface {
	GetAllUserSettings(ctx context.Context, userID string) (map[string]json.RawMessage, error)
	SetUserSetting(ctx context.Context, userID, key string, value json.RawMessage) error
}

// UserService implements user settings with typed accessors.
// No caching — user settings are read infrequently (page load only).
type UserService struct {
	store  UserStore
	logger pkginterfaces.LoggerInterface
	index  map[string]SettingDef
}

// NewUserService creates a new user settings service.
func NewUserService(store UserStore, logger pkginterfaces.LoggerInterface) *UserService {
	return &UserService{
		store:  store,
		logger: logger,
		index:  UserSettingIndex(),
	}
}

func (s *UserService) Start() error { return nil }
func (s *UserService) Stop() error  { return nil }

// Schema returns the Tier 3 setting definitions.
func (s *UserService) Schema() []SettingDef {
	return UserSettings()
}

// GetBool returns a bool user setting value.
func (s *UserService) GetBool(ctx context.Context, userID, key string) (bool, error) {
	v, err := s.get(ctx, userID, key)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("key %q: expected bool, got %T", key, v)
	}
	return b, nil
}

// GetInt returns an int user setting value.
func (s *UserService) GetInt(ctx context.Context, userID, key string) (int, error) {
	v, err := s.get(ctx, userID, key)
	if err != nil {
		return 0, err
	}
	n, ok := toInt(v)
	if !ok {
		return 0, fmt.Errorf("key %q: expected int, got %T", key, v)
	}
	return n, nil
}

// GetString returns a string user setting value.
func (s *UserService) GetString(ctx context.Context, userID, key string) (string, error) {
	v, err := s.get(ctx, userID, key)
	if err != nil {
		return "", err
	}
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("key %q: expected string, got %T", key, v)
	}
	return str, nil
}

// GetAll returns all user settings merged with schema defaults.
func (s *UserService) GetAll(ctx context.Context, userID string) (map[string]any, error) {
	rawMap, err := s.store.GetAllUserSettings(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load user settings: %w", err)
	}

	result := make(map[string]any, len(s.index))
	for key, def := range s.index {
		result[key] = def.Default
	}
	for key, raw := range rawMap {
		var val any
		if err := json.Unmarshal(raw, &val); err != nil {
			continue
		}
		result[key] = val
	}
	return result, nil
}

// Set validates and persists a user setting value.
func (s *UserService) Set(ctx context.Context, userID, key string, value any) error {
	def, ok := s.index[key]
	if !ok {
		return fmt.Errorf("unknown user setting key: %q", key)
	}
	if err := Validate(def, value); err != nil {
		return err
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value for key %q: %w", key, err)
	}

	return s.store.SetUserSetting(ctx, userID, key, raw)
}

// get retrieves a single user setting value with schema default fallback.
func (s *UserService) get(ctx context.Context, userID, key string) (any, error) {
	def, ok := s.index[key]
	if !ok {
		return nil, fmt.Errorf("unknown user setting key: %q", key)
	}

	rawMap, err := s.store.GetAllUserSettings(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load user settings: %w", err)
	}

	if raw, exists := rawMap[key]; exists {
		var val any
		if err := json.Unmarshal(raw, &val); err == nil {
			return val, nil
		}
	}
	return def.Default, nil
}
