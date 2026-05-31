// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetAllInstanceSettings returns all rows from instance_settings.
func (s *Service) GetAllInstanceSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM instance_settings`)
	if err != nil {
		return nil, fmt.Errorf("query instance_settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]json.RawMessage)
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan instance_settings row: %w", err)
		}
		result[key] = value
	}
	return result, rows.Err()
}

// SetInstanceSetting upserts a single instance setting.
func (s *Service) SetInstanceSetting(ctx context.Context, key string, value json.RawMessage) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO instance_settings (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("upsert instance_setting %q: %w", key, err)
	}
	return nil
}

// InsertInstanceSettingIfMissing inserts a setting only if the key doesn't exist.
// Returns true if inserted, false if already existed.
func (s *Service) InsertInstanceSettingIfMissing(ctx context.Context, key string, value json.RawMessage) (bool, error) {
	result, err := s.DB.ExecContext(ctx,
		`INSERT INTO instance_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`,
		key, value,
	)
	if err != nil {
		return false, fmt.Errorf("insert instance_setting %q: %w", key, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetAllUserSettings returns all settings for a specific user.
func (s *Service) GetAllUserSettings(ctx context.Context, userID string) (map[string]json.RawMessage, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT key, value FROM user_settings WHERE user_id = $1`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query user_settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]json.RawMessage)
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan user_settings row: %w", err)
		}
		result[key] = value
	}
	return result, rows.Err()
}

// SetUserSetting upserts a single user setting.
func (s *Service) SetUserSetting(ctx context.Context, userID, key string, value json.RawMessage) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_settings (user_id, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value`,
		userID, key, value,
	)
	if err != nil {
		return fmt.Errorf("upsert user_setting %q for user %q: %w", key, userID, err)
	}
	return nil
}
