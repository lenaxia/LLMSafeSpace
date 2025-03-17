package metrics

import (
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	assert.NotNil(t, svc.requestCounter)
	assert.NotNil(t, svc.requestDuration)
	assert.NotNil(t, svc.responseSize)
	assert.NotNil(t, svc.activeConnections)
	assert.NotNil(t, svc.sandboxesCreated)
	assert.NotNil(t, svc.sandboxesTerminated)
	assert.NotNil(t, svc.executionsTotal)
	assert.NotNil(t, svc.executionDuration)
	assert.NotNil(t, svc.errorsTotal)
	assert.NotNil(t, svc.packageInstalls)
	assert.NotNil(t, svc.fileOperations)
	assert.NotNil(t, svc.resourceUsage)
	assert.NotNil(t, svc.warmPoolHitRatio)
	assert.NotNil(t, svc.warmPoolUtilization)
	assert.NotNil(t, svc.warmPoolScaling)
}

func TestStart(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	err := svc.Start()
	assert.NoError(t, err)
}

func TestStop(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	err := svc.Stop()
	assert.NoError(t, err)
}

func TestRecordRequest(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordRequest("GET", "/api/v1/sandboxes", 200, 100*time.Millisecond, 1024)

	metric, err := svc.requestCounter.GetMetricWithLabelValues("GET", "/api/v1/sandboxes", "200")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))

	metric, err = svc.requestDuration.GetMetricWithLabelValues("GET", "/api/v1/sandboxes")
	assert.NoError(t, err)
	assert.InDelta(t, 0.1, promHistogramValue(metric), 0.01)

	metric, err = svc.responseSize.GetMetricWithLabelValues("GET", "/api/v1/sandboxes")
	assert.NoError(t, err)
	assert.InDelta(t, 1024.0, promHistogramValue(metric), 0.1)
}

func TestRecordSandboxCreation(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordSandboxCreation("python:3.10", true, "user-123")

	metric, err := svc.sandboxesCreated.GetMetricWithLabelValues("python:3.10", "true", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordSandboxTermination(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordSandboxTermination("python:3.10", "timeout")

	metric, err := svc.sandboxesTerminated.GetMetricWithLabelValues("python:3.10", "timeout")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordExecution(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordExecution("code", "python:3.10", "success", "user-123", 500*time.Millisecond)

	metric, err := svc.executionsTotal.GetMetricWithLabelValues("code", "python:3.10", "success", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))

	metric, err = svc.executionDuration.GetMetricWithLabelValues("code", "python:3.10")
	assert.NoError(t, err)
	assert.InDelta(t, 0.5, promHistogramValue(metric), 0.01)
}

func TestRecordError(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordError("api_error", "/api/v1/sandboxes", "404")

	metric, err := svc.errorsTotal.GetMetricWithLabelValues("api_error", "/api/v1/sandboxes", "404")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordPackageInstallation(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordPackageInstallation("python:3.10", "pip", "success")

	metric, err := svc.packageInstalls.GetMetricWithLabelValues("python:3.10", "pip", "success")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordFileOperation(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordFileOperation("upload", "success")

	metric, err := svc.fileOperations.GetMetricWithLabelValues("upload", "success")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordResourceUsage(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordResourceUsage("sandbox-123", 0.5, 1024*1024*1024)

	metric, err := svc.resourceUsage.GetMetricWithLabelValues("sandbox-123", "cpu")
	assert.NoError(t, err)
	assert.Equal(t, 0.5, promGaugeValue(metric))

	metric, err = svc.resourceUsage.GetMetricWithLabelValues("sandbox-123", "memory")
	assert.NoError(t, err)
	assert.Equal(t, float64(1024*1024*1024), promGaugeValue(metric))
}

func TestRecordWarmPoolMetrics(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordWarmPoolMetrics("python:3.10", "pool-1", 0.8)

	metric, err := svc.warmPoolUtilization.GetMetricWithLabelValues("python:3.10", "pool-1")
	assert.NoError(t, err)
	assert.Equal(t, 0.8, promGaugeValue(metric))
}

func TestRecordWarmPoolScaling(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.RecordWarmPoolScaling("python:3.10", "scale_up", "high_demand")

	metric, err := svc.warmPoolScaling.GetMetricWithLabelValues("python:3.10", "scale_up", "high_demand")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestIncrementActiveConnections(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.IncrementActiveConnections("websocket", "user-123")

	metric, err := svc.activeConnections.GetMetricWithLabelValues("websocket", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promGaugeValue(metric))
}

func TestDecrementActiveConnections(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.IncrementActiveConnections("websocket", "user-123")
	svc.DecrementActiveConnections("websocket", "user-123")

	metric, err := svc.activeConnections.GetMetricWithLabelValues("websocket", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 0.0, promGaugeValue(metric))
}

func TestUpdateWarmPoolHitRatio(t *testing.T) {
	logger := logger.New("test")
	svc := New(logger)

	svc.UpdateWarmPoolHitRatio("python:3.10", 0.75)

	metric, err := svc.warmPoolHitRatio.GetMetricWithLabelValues("python:3.10")
	assert.NoError(t, err)
	assert.Equal(t, 0.75, promGaugeValue(metric))
}

// Helper functions to extract values from different Prometheus metric types
func promCounterValue(metric prometheus.Metric) float64 {
	var m dto.Metric
	err := metric.Write(&m)
	if err != nil {
		return 0
	}
	return m.Counter.GetValue()
}

func promGaugeValue(metric prometheus.Metric) float64 {
	var m dto.Metric
	err := metric.Write(&m)
	if err != nil {
		return 0
	}
	return m.Gauge.GetValue()
}

func promHistogramValue(metric prometheus.Metric) float64 {
	var m dto.Metric
	err := metric.Write(&m)
	if err != nil {
		return 0
	}
	
	// For histograms, we return the sum of observations
	// This is a simplification for testing purposes
	return m.Histogram.GetSampleSum()
}
