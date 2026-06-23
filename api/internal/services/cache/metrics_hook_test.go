// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/go-redis/redis/v8"
	dto "github.com/prometheus/client_model/go"

	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
)

func gatherMetricRedis(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	mfs, err := metrics.GatherDefault()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func histogramCountByLabelRedis(t *testing.T, mf *dto.MetricFamily, labelKey, labelVal string) uint64 {
	t.Helper()
	if mf == nil {
		return 0
	}
	var total uint64
	for _, m := range mf.GetMetric() {
		match := false
		for _, l := range m.GetLabel() {
			if l.GetName() == labelKey && l.GetValue() == labelVal {
				match = true
				break
			}
		}
		if match {
			total += m.GetHistogram().GetSampleCount()
		}
	}
	return total
}

func sumCounterByLabelRedis(t *testing.T, mf *dto.MetricFamily, labelKey, labelVal string) float64 {
	t.Helper()
	if mf == nil {
		return 0
	}
	var total float64
	for _, m := range mf.GetMetric() {
		match := false
		for _, l := range m.GetLabel() {
			if l.GetName() == labelKey && l.GetValue() == labelVal {
				match = true
				break
			}
		}
		if match {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

func TestMetricsHook_RecordsCommandDuration(t *testing.T) {
	h := newMetricsHook()
	cmd := redis.NewStringCmd(context.Background(), "GET", "key")

	ctx, err := h.BeforeProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("BeforeProcess: %v", err)
	}
	if err := h.AfterProcess(ctx, cmd); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}

	mf := gatherMetricRedis(t, "llmsafespaces_redis_command_duration_seconds")
	if mf == nil {
		t.Fatal("llmsafespaces_redis_command_duration_seconds not emitted")
	}
	if c := histogramCountByLabelRedis(t, mf, "command", "get"); c == 0 {
		t.Fatal("expected at least one observation for command=get")
	}
}

func TestMetricsHook_RedisNilIsNotAnError(t *testing.T) {
	h := newMetricsHook()
	cmd := redis.NewStringCmd(context.Background(), "GET", "missing")
	cmd.SetErr(redis.Nil)

	beforeMf := gatherMetricRedis(t, "llmsafespaces_redis_errors_total")
	before := sumCounterByLabelRedis(t, beforeMf, "command", "get")

	ctx, err := h.BeforeProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("BeforeProcess: %v", err)
	}
	if err := h.AfterProcess(ctx, cmd); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}

	afterMf := gatherMetricRedis(t, "llmsafespaces_redis_errors_total")
	after := sumCounterByLabelRedis(t, afterMf, "command", "get")
	if after != before {
		t.Fatalf("redis.Nil must not increment errors counter: before=%v after=%v", before, after)
	}
}

func TestMetricsHook_RecordsErrorsCounter(t *testing.T) {
	h := newMetricsHook()
	cmd := redis.NewStringCmd(context.Background(), "GET", "key")
	cmd.SetErr(errors.New("connection refused"))

	beforeMf := gatherMetricRedis(t, "llmsafespaces_redis_errors_total")
	before := sumCounterByLabelRedis(t, beforeMf, "command", "get")

	ctx, err := h.BeforeProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("BeforeProcess: %v", err)
	}
	if err := h.AfterProcess(ctx, cmd); err != nil {
		t.Fatalf("AfterProcess: %v", err)
	}

	afterMf := gatherMetricRedis(t, "llmsafespaces_redis_errors_total")
	after := sumCounterByLabelRedis(t, afterMf, "command", "get")
	if after-before < 1 {
		t.Fatalf("expected error counter to increment by >=1; got delta=%v", after-before)
	}
}

func TestMetricsHook_PipelineRecordsEachCommand(t *testing.T) {
	h := newMetricsHook()
	cmds := []redis.Cmder{
		redis.NewStringCmd(context.Background(), "GET", "a"),
		redis.NewStringCmd(context.Background(), "SET", "b", "1"),
	}
	ctx, err := h.BeforeProcessPipeline(context.Background(), cmds)
	if err != nil {
		t.Fatalf("BeforeProcessPipeline: %v", err)
	}
	if err := h.AfterProcessPipeline(ctx, cmds); err != nil {
		t.Fatalf("AfterProcessPipeline: %v", err)
	}

	mf := gatherMetricRedis(t, "llmsafespaces_redis_command_duration_seconds")
	if mf == nil {
		t.Fatal("llmsafespaces_redis_command_duration_seconds not emitted")
	}
	if c := histogramCountByLabelRedis(t, mf, "command", "get"); c == 0 {
		t.Fatal("expected at least one get observation from pipeline")
	}
	if c := histogramCountByLabelRedis(t, mf, "command", "set"); c == 0 {
		t.Fatal("expected at least one set observation from pipeline")
	}
}
