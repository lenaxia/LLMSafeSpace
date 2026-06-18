// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package wsstate

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

// Compile-time assertion that RedisStore implements Store.
var _ Store = (*RedisStore)(nil)

// DefaultActiveSessTTL is the auto-recovery TTL for stuck active-session
// entries. If a session is added but never removed (process crash,
// network partition), the entry expires after this duration so the
// workspace doesn't stay stuck — the multi-replica fix for the
// 2026-06-16 incident. 30 minutes matches the design spec.
const DefaultActiveSessTTL = 30 * time.Minute

// DefaultDeletedTTL is the TTL for per-session tombstones. Each
// tombstone expires independently (per-key TTL, not a shared SET TTL).
// After expiry, late SSE events for that session are no longer
// suppressed — but by then the session has been gone long enough that
// any late event is extremely unlikely. 30 minutes matches the design
// spec and is the same duration as activeSess TTL.
const DefaultDeletedTTL = 30 * time.Minute

// DefaultPasswordTTL is the TTL for cached workspace passwords.
// Passwords are stable (only change on workspace recreate), so the TTL
// can be longer than for active sessions or tombstones. 1 hour matches
// the design spec. After TTL expiry, the next request re-fetches from
// K8s (password may have rotated).
const DefaultPasswordTTL = 1 * time.Hour

// checkAndAddScript atomically checks the active-session set size and
// adds the session ID if there's room. The atomicity is what makes this
// safe across replicas: two concurrent calls cannot both observe
// size == maxSessions and both succeed. Lua scripts run as a single
// indivisible command in Redis, so the SISMEMBER+SCARD+SADD sequence
// is race-free.
//
// Returns 1 if added OR already present (idempotent); 0 if blocked by
// the maxSessions limit.
var checkAndAddScript = redis.NewScript(`
-- KEYS[1] = "ws:{workspace_id}:active"
-- ARGV[1] = sessionID
-- ARGV[2] = maxSessions
-- ARGV[3] = ttlSeconds
-- Returns: 1 if added/already-present, 0 if rejected by limit

local key = KEYS[1]
local sessionID = ARGV[1]
local maxSessions = tonumber(ARGV[2])
local ttlSeconds = tonumber(ARGV[3])

if redis.call('SISMEMBER', key, sessionID) == 1 then
    redis.call('EXPIRE', key, ttlSeconds)
    return 1
end

local count = redis.call('SCARD', key)
if count >= maxSessions then
    return 0
end

redis.call('SADD', key, sessionID)
redis.call('EXPIRE', key, ttlSeconds)
return 1
`)

// RedisStore is the multi-replica-safe implementation of Store. It
// backs the active-session set, deleted-session tombstones, and
// password cache with Redis; the remaining state sections (wsConfig,
// priorPhase, parentBackfilled) continue to be served by the embedded
// InMemoryStore; their migration to Redis is the subject of US-45.6
// through US-45.8.
//
// Fail-open policy (per design): if Redis is unreachable,
// CheckAndAddActiveSession returns true (allow the request) and records
// the error via the metrics. Read methods (IsSessionActive,
// ActiveSessionCount, GetActiveSessions) return their safe defaults
// (false, 0, nil) under outage — see each method's doc comment.
//
// All methods are safe for concurrent use.
type RedisStore struct {
	// client is borrowed — its lifecycle is managed by the caller
	// (typically the cache service). RedisStore does not close it.
	client *redis.Client

	// activeSessTTL is the auto-recovery TTL for stuck active-session
	// entries. Refreshed on every successful CheckAndAddActiveSession.
	activeSessTTL time.Duration

	// deletedTTL is the per-key TTL for session tombstones. Each
	// tombstone expires independently.
	deletedTTL time.Duration

	// passwordTTL is the TTL for cached workspace passwords. Longer than
	// activeSessTTL/deletedTTL because passwords are stable.
	passwordTTL time.Duration

	// logger records fail-open events. Optional — if nil, errors are
	// surfaced only via Prometheus metrics.
	logger pkginterfaces.LoggerInterface

	// inMemory serves the un-migrated sections of the Store interface
	// (everything except activeSess). Each section is migrated to Redis
	// in its own story; when all are migrated (US-45.9), this field is
	// removed.
	inMemory *InMemoryStore

	// Prometheus metrics required by US-45.2.
	opDuration          *prometheus.HistogramVec
	errorsTotal         *prometheus.CounterVec
	activeSessionsGauge *prometheus.GaugeVec
}

// NewRedisStore returns a Store backed by Redis for active sessions and
// by InMemoryStore for the remaining (not-yet-migrated) sections. The
// active-session TTL is set to DefaultActiveSessTTL.
func NewRedisStore(client *redis.Client, activeSessTTL time.Duration) *RedisStore {
	return NewRedisStoreWithLogger(client, activeSessTTL, nil)
}

// NewRedisStoreWithLogger is like NewRedisStore but also accepts a
// logger for fail-open event recording. The logger may be nil —
// Prometheus metrics are recorded regardless.
func NewRedisStoreWithLogger(client *redis.Client, activeSessTTL time.Duration, logger pkginterfaces.LoggerInterface) *RedisStore {
	if activeSessTTL <= 0 {
		activeSessTTL = DefaultActiveSessTTL
	}
	registerMetrics()
	return &RedisStore{
		client:              client,
		activeSessTTL:       activeSessTTL,
		deletedTTL:          DefaultDeletedTTL,
		passwordTTL:         DefaultPasswordTTL,
		logger:              logger,
		inMemory:            NewInMemoryStore(),
		opDuration:          pkgOpDuration,
		errorsTotal:         pkgErrorsTotal,
		activeSessionsGauge: pkgActiveSessionsGauge,
	}
}

// Package-level Prometheus metrics. Registered once via sync.Once
// because the Prometheus default registry rejects duplicate
// registrations — each test creates a fresh RedisStore, so per-store
// metric fields would panic on the second construction.
var (
	metricsOnce sync.Once

	pkgOpDuration          *prometheus.HistogramVec
	pkgErrorsTotal         *prometheus.CounterVec
	pkgActiveSessionsGauge *prometheus.GaugeVec
)

func registerMetrics() {
	metricsOnce.Do(func() {
		pkgOpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ws_state_op_duration_seconds",
			Help:    "wsstate Store operation latency by operation and result",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12),
		}, []string{"op", "result"})

		pkgErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "ws_state_errors_total",
			Help: "wsstate Store operation errors by operation",
		}, []string{"op"})

		pkgActiveSessionsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ws_state_active_sessions",
			Help: "wsstate active session count per workspace (sampled on writes)",
		}, []string{"workspace_id"})
	})
}

// observeOp records an operation's duration and result. Callers must
// pass a non-zero start time; we avoid time.Since ambiguity by taking
// the start as a parameter.
func (s *RedisStore) observeOp(op, result string, start time.Time) {
	if s.opDuration == nil {
		return
	}
	s.opDuration.WithLabelValues(op, result).Observe(time.Since(start).Seconds())
}

func (s *RedisStore) recordError(op string) {
	if s.errorsTotal == nil {
		return
	}
	s.errorsTotal.WithLabelValues(op).Inc()
}

// activeKey returns the canonical Redis key for a workspace's active
// session set. The {workspace_id} hash tag forces all keys for a
// workspace to land on the same Redis shard — enables future cluster
// migration with zero code change.
func activeSessKey(workspaceID string) string {
	return fmt.Sprintf("ws:{%s}:active", workspaceID)
}

// --- Active session tracking (Redis-backed) ---

// CheckAndAddActiveSession atomically adds sessionID to the workspace's
// active set if there's room. Fail-open: if Redis is unreachable,
// returns true and records the error. The rationale (per design):
// better to allow a request than block legit traffic when Redis hiccups.
func (s *RedisStore) CheckAndAddActiveSession(workspaceID, sessionID string, maxSessions int) bool {
	const op = "check_and_add_active_session"
	start := time.Now()

	res, err := checkAndAddScript.Run(context.Background(), s.client,
		[]string{activeSessKey(workspaceID)},
		sessionID, maxSessions, int(s.activeSessTTL.Seconds())).Result()
	if err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Warn("wsstate: Redis CheckAndAddActiveSession failed, failing OPEN",
				"error", err, "workspace_id", workspaceID, "session_id", sessionID)
		}
		return true
	}

	allowed, ok := res.(int64)
	if !ok {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Error("wsstate: unexpected CheckAndAdd result type", fmt.Errorf("got %T", res),
				"workspace_id", workspaceID, "session_id", sessionID)
		}
		return true
	}

	if allowed == 1 {
		s.observeOp(op, "allowed", start)
		if s.activeSessionsGauge != nil {
			// Sample the gauge on every successful write. Cheaper than a
			// separate polling loop, and the value is fresh.
			n := s.ActiveSessionCount(workspaceID)
			s.activeSessionsGauge.WithLabelValues(workspaceID).Set(float64(n))
		}
		return true
	}
	s.observeOp(op, "rejected", start)
	return false
}

// RemoveActiveSession removes sessionID from the workspace's active set.
// If the set becomes empty, the Redis key is deleted so it does not
// linger as an orphan with TTL countdown.
//
// The SREM, SCARD-check, and conditional DEL run inside a single Lua
// script so the entire operation is atomic. Without atomicity a race
// could exist: between SREM and a separate DEL-on-empty check, another
// replica could SADD a new session; the subsequent DEL would erase it.
//
// On transition to empty the Prometheus gauge label is cleaned up via
// DeleteLabelValues — without this, workspaces that churn through
// create/suspend/terminate cycles would accumulate orphan time series
// forever (workspace_id is a UUID, so cardinality is unbounded).
func (s *RedisStore) RemoveActiveSession(workspaceID, sessionID string) {
	const op = "remove_active_session"
	start := time.Now()
	key := activeSessKey(workspaceID)

	res, err := removeActiveScript.Run(context.Background(), s.client,
		[]string{key}, sessionID).Result()
	if err != nil && err != redis.Nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Warn("wsstate: Redis RemoveActiveSession failed",
				"error", err, "workspace_id", workspaceID, "session_id", sessionID)
		}
		return
	}
	s.observeOp(op, "ok", start)

	// Clean up the Prometheus gauge label when the workspace's set
	// becomes empty. removeActiveScript returns the remaining size
	// (0 if the key was deleted). This bounds metric cardinality.
	remaining, _ := res.(int64)
	if remaining == 0 && s.activeSessionsGauge != nil {
		s.activeSessionsGauge.DeleteLabelValues(workspaceID)
	}
}

// removeActiveScript atomically removes a session and deletes the key
// if the set is now empty. Returns the remaining size (0 if key deleted).
var removeActiveScript = redis.NewScript(`
-- KEYS[1] = "ws:{workspace_id}:active"
-- ARGV[1] = sessionID
local key = KEYS[1]
redis.call('SREM', key, ARGV[1])
if redis.call('SCARD', key) == 0 then
    redis.call('DEL', key)
    return 0
end
return redis.call('SCARD', key)
`)

// IsSessionActive reports whether sessionID is in the workspace's
// active set. Returns false on Redis error (do not trap the user in 409
// based on possibly-stale state).
func (s *RedisStore) IsSessionActive(workspaceID, sessionID string) bool {
	const op = "is_session_active"
	start := time.Now()
	n, err := s.client.SIsMember(context.Background(), activeSessKey(workspaceID), sessionID).Result()
	if err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		return false
	}
	s.observeOp(op, "ok", start)
	return n
}

// ActiveSessionCount returns the number of sessions currently in the
// workspace's active set. Returns 0 on Redis error.
func (s *RedisStore) ActiveSessionCount(workspaceID string) int {
	const op = "active_session_count"
	start := time.Now()
	n, err := s.client.SCard(context.Background(), activeSessKey(workspaceID)).Result()
	if err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		return 0
	}
	s.observeOp(op, "ok", start)
	return int(n)
}

// GetActiveSessions returns the IDs of all sessions currently in the
// workspace's active set. Returns nil on Redis error or empty set.
func (s *RedisStore) GetActiveSessions(workspaceID string) []string {
	const op = "get_active_sessions"
	start := time.Now()
	members, err := s.client.SMembers(context.Background(), activeSessKey(workspaceID)).Result()
	if err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		return nil
	}
	s.observeOp(op, "ok", start)
	if len(members) == 0 {
		return nil
	}
	return members
}

// ClearActiveSessions deletes the workspace's entire active set,
// removing the Redis key entirely so no orphan TTL countdown lingers.
// Also cleans up the Prometheus gauge label to bound cardinality.
func (s *RedisStore) ClearActiveSessions(workspaceID string) {
	const op = "clear_active_sessions"
	start := time.Now()
	if err := s.client.Del(context.Background(), activeSessKey(workspaceID)).Err(); err != nil && err != redis.Nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Warn("wsstate: Redis ClearActiveSessions failed",
				"error", err, "workspace_id", workspaceID)
		}
		return
	}
	s.observeOp(op, "ok", start)
	if s.activeSessionsGauge != nil {
		s.activeSessionsGauge.DeleteLabelValues(workspaceID)
	}
}

// --- InvalidateAll (overrides to clear both Redis and InMemory) ---

// InvalidateAll clears both the Redis-backed state (active sessions,
// deleted tombstones, password cache) and the InMemoryStore-backed
// state (workspace config, parent backfill). priorPhase is intentionally
// preserved (per US-45.1 contract — onPhaseChange relies on it).
func (s *RedisStore) InvalidateAll(workspaceID string) {
	// Redis-backed: clear active sessions, deleted tombstones, password.
	s.ClearActiveSessions(workspaceID)
	s.ClearDeletedSessions(workspaceID)
	s.InvalidatePassword(workspaceID)
	// InMemoryStore-backed: clear config, parent backfill. We call the
	// individual methods rather than inMemory.InvalidateAll because that
	// would also call inMemory.ClearActiveSessions/ClearDeletedSessions/
	// InvalidatePassword — no-ops (state is on Redis) but wasteful.
	// priorPhase is preserved per US-45.1 contract.
	s.inMemory.ClearActiveSessions(workspaceID)
	s.inMemory.ClearDeletedSessions(workspaceID)
	s.inMemory.InvalidatePassword(workspaceID)
	s.inMemory.InvalidateWorkspaceConfig(workspaceID)
	s.inMemory.DeleteParentBackfilled(workspaceID)
}

// --- Deleted-session tombstones (Redis-backed, US-45.3) ---
//
// Tombstones prevent late SSE events from opencode from re-inserting a
// deleted session into session_index. Moving them to Redis ensures the
// suppression is cluster-wide.
//
// Fail-CLOSED policy (intentional opposite of activeSess fail-OPEN): if
// Redis is unreachable, IsSessionDeleted returns TRUE (assume deleted to
// prevent zombie resurrection). The rationale (per design): "If we can't
// verify, assume deleted; user can recreate session" — data integrity >
// availability here.

func deletedSessKey(workspaceID, sessionID string) string {
	return fmt.Sprintf("ws:{%s}:deleted:%s", workspaceID, sessionID)
}

// MarkSessionDeleted records a per-session tombstone in Redis with TTL.
// Silently fails on Redis error — the tombstone is not recorded, but the
// system continues. When Redis recovers, the session can be re-deleted.
func (s *RedisStore) MarkSessionDeleted(workspaceID, sessionID string) {
	const op = "mark_session_deleted"
	start := time.Now()
	key := deletedSessKey(workspaceID, sessionID)
	if err := s.client.Set(context.Background(), key, "1", s.deletedTTL).Err(); err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Warn("wsstate: Redis MarkSessionDeleted failed",
				"error", err, "workspace_id", workspaceID, "session_id", sessionID)
		}
		return
	}
	s.observeOp(op, "ok", start)
}

// IsSessionDeleted reports whether the session was recently deleted via
// the API. Fail-CLOSED: returns TRUE on Redis error (assume deleted to
// prevent zombie session resurrection).
func (s *RedisStore) IsSessionDeleted(workspaceID, sessionID string) bool {
	const op = "is_session_deleted"
	start := time.Now()
	n, err := s.client.Exists(context.Background(), deletedSessKey(workspaceID, sessionID)).Result()
	if err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		return true
	}
	s.observeOp(op, "ok", start)
	return n > 0
}

// ClearDeletedSessions removes all tombstones for the workspace. Uses
// SCAN to find keys matching `ws:{workspace_id}:deleted:*` and DELs them
// in batches. No-op on Redis error (the tombstones will expire via TTL).
func (s *RedisStore) ClearDeletedSessions(workspaceID string) {
	const op = "clear_deleted_sessions"
	start := time.Now()
	pattern := fmt.Sprintf("ws:{%s}:deleted:*", workspaceID)
	var cursor uint64
	for {
		keys, next, err := s.client.Scan(context.Background(), cursor, pattern, 100).Result()
		if err != nil {
			s.recordError(op)
			s.observeOp(op, "error", start)
			if s.logger != nil {
				s.logger.Warn("wsstate: Redis ClearDeletedSessions scan failed",
					"error", err, "workspace_id", workspaceID)
			}
			return
		}
		if len(keys) > 0 {
			if err := s.client.Del(context.Background(), keys...).Err(); err != nil {
				s.recordError(op)
				s.observeOp(op, "error", start)
				if s.logger != nil {
					s.logger.Warn("wsstate: Redis ClearDeletedSessions del failed",
						"error", err, "workspace_id", workspaceID)
				}
				return
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	s.observeOp(op, "ok", start)
}

// --- Workspace password cache (Redis-backed, US-45.4) ---
//
// Passwords are stable (only change on workspace recreate). Moving them
// to Redis eliminates per-replica staleness on phase changes: a 401 on
// replica A that invalidates the cache is now visible to all replicas.
//
// Fail-through-to-K8s policy: Redis is a cache, the source of truth is
// the K8s Secret. On Redis error, GetCachedPassword returns (empty, false)
// so the caller (ProxyHandler.getPassword) falls back to fetching the
// K8s Secret directly. This is NOT fail-closed (no false data) and NOT
// fail-open (no return true) — it is "fail-through" to the source of truth.
//
// The K8s Secret fetch stays in ProxyHandler.getPassword so the store
// remains pure-state (no I/O dependencies). The store's SetCachedPassword
// is called only after a successful K8s fetch to populate the shared cache.

func passwordCacheKey(workspaceID string) string {
	return fmt.Sprintf("ws:{%s}:pw", workspaceID)
}

// GetCachedPassword returns the cached password for the workspace, if
// present. Cache-only — never returns false data on Redis error. Returns
// ("", false) on miss OR on Redis error so the caller falls through to
// the K8s Secret fetch.
func (s *RedisStore) GetCachedPassword(workspaceID string) (string, bool) {
	const op = "get_cached_password"
	start := time.Now()
	pw, err := s.client.Get(context.Background(), passwordCacheKey(workspaceID)).Result()
	if err != nil {
		// redis.Nil = key not found (cache miss) — not an error.
		if err != redis.Nil {
			s.recordError(op)
			s.observeOp(op, "error", start)
			if s.logger != nil {
				s.logger.Warn("wsstate: Redis GetCachedPassword failed — falling through to K8s",
					"error", err, "workspace_id", workspaceID)
			}
		} else {
			s.observeOp(op, "miss", start)
		}
		return "", false
	}
	s.observeOp(op, "hit", start)
	return pw, true
}

// SetCachedPassword populates the password cache for the workspace.
// Silently fails on Redis error — the next read returns a miss and
// falls through to K8s. Idempotent: re-setting the same password
// refreshes the TTL.
func (s *RedisStore) SetCachedPassword(workspaceID, password string) {
	const op = "set_cached_password"
	start := time.Now()
	if err := s.client.Set(context.Background(), passwordCacheKey(workspaceID), password, s.passwordTTL).Err(); err != nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Warn("wsstate: Redis SetCachedPassword failed",
				"error", err, "workspace_id", workspaceID)
		}
		return
	}
	s.observeOp(op, "ok", start)
}

// InvalidatePassword clears the cached password for the workspace.
// DEL is the single source of truth — replicas hitting Redis on miss
// fall through to K8s. No pubsub needed (per design: replicas hit Redis
// on every request anyway).
func (s *RedisStore) InvalidatePassword(workspaceID string) {
	const op = "invalidate_password"
	start := time.Now()
	if err := s.client.Del(context.Background(), passwordCacheKey(workspaceID)).Err(); err != nil && err != redis.Nil {
		s.recordError(op)
		s.observeOp(op, "error", start)
		if s.logger != nil {
			s.logger.Warn("wsstate: Redis InvalidatePassword failed",
				"error", err, "workspace_id", workspaceID)
		}
		return
	}
	s.observeOp(op, "ok", start)
}

// --- Workspace config cache (delegated to InMemoryStore) ---
// US-45.6 will move this to Redis.

func (s *RedisStore) GetWorkspaceConfig(workspaceID string) (Config, bool) {
	return s.inMemory.GetWorkspaceConfig(workspaceID)
}

func (s *RedisStore) SetWorkspaceConfig(workspaceID string, cfg Config) {
	s.inMemory.SetWorkspaceConfig(workspaceID, cfg)
}

func (s *RedisStore) InvalidateWorkspaceConfig(workspaceID string) {
	s.inMemory.InvalidateWorkspaceConfig(workspaceID)
}

// --- Prior phase tracking (delegated to InMemoryStore) ---
// US-45.7 will move this to Redis.

func (s *RedisStore) GetPriorPhase(workspaceID string) (string, bool) {
	return s.inMemory.GetPriorPhase(workspaceID)
}

func (s *RedisStore) SetPriorPhase(workspaceID, phase string) {
	s.inMemory.SetPriorPhase(workspaceID, phase)
}

func (s *RedisStore) DeletePriorPhase(workspaceID string) {
	s.inMemory.DeletePriorPhase(workspaceID)
}

// --- Parent-backfill marker (delegated to InMemoryStore) ---
// US-45.8 will move this to Redis.

func (s *RedisStore) GetParentBackfilled(workspaceID string) bool {
	return s.inMemory.GetParentBackfilled(workspaceID)
}

func (s *RedisStore) SetParentBackfilled(workspaceID string) {
	s.inMemory.SetParentBackfilled(workspaceID)
}

func (s *RedisStore) DeleteParentBackfilled(workspaceID string) {
	s.inMemory.DeleteParentBackfilled(workspaceID)
}
