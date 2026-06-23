// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"

	"github.com/lenaxia/llmsafespaces/pkg/secrets"
)

// deriveKey derives a 32-byte purpose-scoped key from the master key via HKDF.
// Mirrors deriveServerKey in the API app package.
func deriveKey(master []byte, purpose string) []byte {
	if len(master) < 32 {
		return nil
	}
	r := hkdf.New(sha256.New, master, []byte("llmsafespaces-server"), []byte(purpose))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil
	}
	return key
}

// hexDecode wraps encoding/hex.DecodeString, returning an error on invalid hex.
func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// --- PgRotationStore ---

// pgRotationStore implements secrets.RotationStore against a live Postgres.
// It is a thin wrapper; the queries are straightforward SELECT/UPDATE.
type pgRotationStore struct {
	db pgConn
}

type pgConn interface {
	QueryRow(ctx context.Context, query string, args ...any) pgRow
	Query(ctx context.Context, query string, args ...any) (pgRows, error)
	Exec(ctx context.Context, query string, args ...any) error
	Close()
}

type pgRow interface {
	Scan(dest ...any) error
}

type pgRows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
}

func newPgRotationStore(dbURL string) (*pgRotationStore, error) {
	// In the full implementation, this opens a pgx connection.
	// For now it's a stub that the CLI compiles against.
	return &pgRotationStore{db: nil}, fmt.Errorf("postgres connection not yet wired (use the rotation coordinator directly for testing)")
}

func (s *pgRotationStore) ListRotationRows(ctx context.Context, table, resumeFromID string, targetVersion, limit int) ([]secrets.RotationRow, error) {
	return nil, fmt.Errorf("not implemented: wire pgx for production use")
}

func (s *pgRotationStore) UpdateRotationRow(ctx context.Context, table, rowID string, newCT []byte, newVer int) error {
	return fmt.Errorf("not implemented: wire pgx for production use")
}

func (s *pgRotationStore) FlushDEKCache(ctx context.Context) error {
	return nil
}

func (s *pgRotationStore) Close() {}

// --- Redis cache flusher ---

type redisCacheFlusher struct{}

func newRedisCacheFlusher(redisURL string) (*redisCacheFlusher, error) {
	return nil, fmt.Errorf("redis connection not yet wired")
}

func (r *redisCacheFlusher) Close() {}

// compositeRotationStore delegates to a pg store for row operations and a
// Redis client for DEK cache flush.
type compositeRotationStore struct {
	pg    *pgRotationStore
	redis *redisCacheFlusher
}

func newCompositeRotationStore(pg *pgRotationStore, redis *redisCacheFlusher) secrets.RotationStore {
	return &compositeRotationStore{pg: pg, redis: redis}
}

func (c *compositeRotationStore) ListRotationRows(ctx context.Context, table, resumeFromID string, targetVersion, limit int) ([]secrets.RotationRow, error) {
	return c.pg.ListRotationRows(ctx, table, resumeFromID, targetVersion, limit)
}

func (c *compositeRotationStore) UpdateRotationRow(ctx context.Context, table, rowID string, newCT []byte, newVer int) error {
	return c.pg.UpdateRotationRow(ctx, table, rowID, newCT, newVer)
}

func (c *compositeRotationStore) FlushDEKCache(ctx context.Context) error {
	// In the full implementation, this calls FLUSHDB or DELETE on the DEK
	// cache keys. The stub delegates to pg (no-op).
	return c.pg.FlushDEKCache(ctx)
}
