package metrics

import (
	"os"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

var metricsService *Service

func TestMain(m *testing.M) {
	// Initialize logger and metrics service
	logger, _ := logger.New(true, "debug", "console")
	metricsService = New(logger)

	// Run tests
	exitCode := m.Run()

	// Clean up
	metricsService.Stop()

	os.Exit(exitCode)
}

func TestNew(t *testing.T) {
	assert.NotNil(t, metricsService.requestCounter)
	assert.NotNil(t, metricsService.requestDuration)
	assert.NotNil(t, metricsService.responseSize)
	assert.NotNil(t, metricsService.activeConnections)
	assert.NotNil(t, metricsService.sandboxesCreated)
	assert.NotNil(t, metricsService.sandboxesTerminated)
	assert.NotNil(t, metricsService.executionsTotal)
	assert.NotNil(t, metricsService.executionDuration)
	assert.NotNil(t, metricsService.errorsTotal)
	assert.NotNil(t, metricsService.packageInstalls)
	assert.NotNil(t, metricsService.fileOperations)
	assert.NotNil(t, metricsService.resourceUsage)
	assert.NotNil(t, metricsService.warmPoolHitRatio)
	assert.NotNil(t, metricsService.warmPoolUtilization)
	assert.NotNil(t, metricsService.warmPoolScaling)
}

func TestStart(t *testing.T) {
	err := metricsService.Start()
	assert.NoError(t, err)
}

func TestStop(t *testing.T) {
	err := metricsService.Stop()
	assert.NoError(t, err)
}

func TestRecordRequest(t *testing.T) {
	metricsService.RecordRequest("GET", "/api/v1/sandboxes", 200, 100*time.Millisecond, 1024)

	// Test request counter
	metric, err := metricsService.requestCounter.GetMetricWithLabelValues("GET", "/api/v1/sandboxes", "200")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))

	// Test request duration histogram
	observer, err := metricsService.requestDuration.GetMetricWithLabelValues("GET", "/api/v1/sandboxes")
	assert.NoError(t, err)
	histogram, ok := observer.(prometheus.Histogram)
	assert.True(t, ok, "Expected Histogram type")
	assert.InDelta(t, 0.1, promHistogramValue(histogram), 0.01)

	// Test response size histogram
	observer, err = metricsService.responseSize.GetMetricWithLabelValues("GET", "/api/v1/sandboxes")
	assert.NoError(t, err)
	histogram, ok = observer.(prometheus.Histogram)
	assert.True(t, ok, "Expected Histogram type")
	assert.InDelta(t, 1024.0, promHistogramValue(histogram), 0.1)
}

func TestRecordSandboxCreation(t *testing.T) {
	metricsService.RecordSandboxCreation("python:3.10", true, "user-123")

	metric, err := metricsService.sandboxesCreated.GetMetricWithLabelValues("python:3.10", "true", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordSandboxTermination(t *testing.T) {
	metricsService.RecordSandboxTermination("python:3.10", "timeout")

	metric, err := metricsService.sandboxesTerminated.GetMetricWithLabelValues("python:3.10", "timeout")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordExecution(t *testing.T) {
	metricsService.RecordExecution("code", "python:3.10", "success", "user-123", 500*time.Millisecond)

	// Test executions counter
	metric, err := metricsService.executionsTotal.GetMetricWithLabelValues("code", "python:3.10", "success", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))

	// Test execution duration histogram
	observer, err := metricsService.executionDuration.GetMetricWithLabelValues("code", "python:3.10")
	assert.NoError(t, err)
	histogram, ok := observer.(prometheus.Histogram)
	assert.True(t, ok, "Expected Histogram type")
	assert.InDelta(t, 0.5, promHistogramValue(histogram), 0.01)
}

func TestRecordError(t *testing.T) {
	metricsService.RecordError("api_error", "/api/v1/sandboxes", "404")

	metric, err := metricsService.errorsTotal.GetMetricWithLabelValues("api_error", "/api/v1/sandboxes", "404")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordPackageInstallation(t *testing.T) {
	metricsService.RecordPackageInstallation("python:3.10", "pip", "success")

	metric, err := metricsService.packageInstalls.GetMetricWithLabelValues("python:3.10", "pip", "success")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordFileOperation(t *testing.T) {
	metricsService.RecordFileOperation("upload", "success")

	metric, err := metricsService.fileOperations.GetMetricWithLabelValues("upload", "success")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestRecordResourceUsage(t *testing.T) {
	metricsService.RecordResourceUsage("sandbox-123", 0.5, 1024*1024*1024)

	metric, err := metricsService.resourceUsage.GetMetricWithLabelValues("sandbox-123", "cpu")
	assert.NoError(t, err)
	assert.Equal(t, 0.5, promGaugeValue(metric))

	metric, err = metricsService.resourceUsage.GetMetricWithLabelValues("sandbox-123", "memory")
	assert.NoError(t, err)
	assert.Equal(t, float64(1024*1024*1024), promGaugeValue(metric))
}

func TestRecordWarmPoolMetrics(t *testing.T) {
	metricsService.RecordWarmPoolMetrics("python:3.10", "pool-1", 0.8)

	metric, err := metricsService.warmPoolUtilization.GetMetricWithLabelValues("python:3.10", "pool-1")
	assert.NoError(t, err)
	assert.Equal(t, 0.8, promGaugeValue(metric))
}

func TestRecordWarmPoolScaling(t *testing.T) {
	metricsService.RecordWarmPoolScaling("python:3.10", "scale_up", "high_demand")

	metric, err := metricsService.warmPoolScaling.GetMetricWithLabelValues("python:3.10", "scale_up", "high_demand")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promCounterValue(metric))
}

func TestIncrementActiveConnections(t *testing.T) {
	metricsService.IncrementActiveConnections("websocket", "user-123")

	metric, err := metricsService.activeConnections.GetMetricWithLabelValues("websocket", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 1.0, promGaugeValue(metric))
}

func TestDecrementActiveConnections(t *testing.T) {
	metricsService.IncrementActiveConnections("websocket", "user-123")
	metricsService.DecrementActiveConnections("websocket", "user-123")

	metric, err := metricsService.activeConnections.GetMetricWithLabelValues("websocket", "user-123")
	assert.NoError(t, err)
	assert.Equal(t, 0.0, promGaugeValue(metric))
}

func TestUpdateWarmPoolHitRatio(t *testing.T) {
	metricsService.UpdateWarmPoolHitRatio("python:3.10", 0.75)

	metric, err := metricsService.warmPoolHitRatio.GetMetricWithLabelValues("python:3.10")
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
