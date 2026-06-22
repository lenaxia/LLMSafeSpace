// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// metrics_hook.go — go-redis v8 hook that records per-command latency and
// errors on the prometheus default registry.
//
// Attached at cache.New() so every command issued through the shared
// *redis.Client (including the pipelined MQ + DEK paths that reuse the
// pool via GetClient()) flows through one tracer. Without this the
// redis-related dashboard panels remain empty regardless of traffic.

package cache

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
)

// metricsHook implements redis.Hook (go-redis v8). The hook is stateless;
// per-command timing is carried on the per-call context returned from
// BeforeProcess.
type metricsHook struct{}

// metricsHookCtxKey scopes the per-command start time so it does not collide
// with other context values the application sets.
type metricsHookCtxKey struct{}

func newMetricsHook() *metricsHook { return &metricsHook{} }

// NewMetricsHook returns a redis.Hook that records command latency and
// errors on the prometheus default registry. Exported so other Redis
// clients in the binary (e.g. the DEK cache client constructed in
// internal/app) can attach the same hook for consistent dashboard data.
func NewMetricsHook() redis.Hook { return newMetricsHook() }

// Compile-time assertion: metricsHook must implement redis.Hook.
var _ redis.Hook = (*metricsHook)(nil)

// BeforeProcess records the start time on the returned context; AfterProcess
// reads it back and emits the histogram observation.
func (h *metricsHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return context.WithValue(ctx, metricsHookCtxKey{}, time.Now()), nil
}

// AfterProcess records latency and (on failure) the errors counter for a
// single command. Returning a non-nil error here is forbidden by the
// redis.Hook contract and would mask a real driver error, so this method
// always returns nil regardless of metric-recording outcomes.
func (h *metricsHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	h.observe(ctx, cmd)
	return nil
}

// BeforeProcessPipeline mirrors BeforeProcess for pipeline calls: a single
// start time is recorded and divided across the pipeline's commands at
// AfterProcessPipeline time.
func (h *metricsHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return context.WithValue(ctx, metricsHookCtxKey{}, time.Now()), nil
}

// AfterProcessPipeline records one observation per command in the pipeline.
// All commands share the same elapsed time because go-redis pipelines them
// over a single round trip; per-command precision is not available without
// invasive driver changes.
func (h *metricsHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	for _, c := range cmds {
		h.observe(ctx, c)
	}
	return nil
}

func (h *metricsHook) observe(ctx context.Context, cmd redis.Cmder) {
	start, ok := ctx.Value(metricsHookCtxKey{}).(time.Time)
	if !ok {
		return
	}
	command := normaliseCommandName(cmd.Name())
	metrics.RecordRedisCommandDuration(command, time.Since(start))

	if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
		metrics.RecordRedisError(command)
	}
}

// normaliseCommandName lower-cases the command and folds variants down to
// the underlying root. go-redis returns commands like "subscribe" or
// "cluster info" with multi-word names; we keep only the first token to
// keep label cardinality bounded.
func normaliseCommandName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(name, ' '); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "unknown"
	}
	return name
}
