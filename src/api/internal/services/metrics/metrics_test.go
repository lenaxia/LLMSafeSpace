package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordRequest(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	
	// Create a new metrics service
	service := New()
	
	// Record a request
	method := "GET"
	endpoint := "/api/v1/sandboxes"
	status := 200
	duration := 100 * time.Millisecond
	size := 1024
	
	service.RecordRequest(method, endpoint, status, duration, size)
	
	// Verify the request counter was incremented
	counter, err := service.requestCounter.GetMetricWithLabelValues(method, endpoint, "200")
	if err != nil {
		t.Fatalf("Error getting metric: %v", err)
	}
	
	count := testutil.ToFloat64(counter)
	if count != 1 {
		t.Errorf("Expected request counter to be 1, got %f", count)
	}
	
	// Record another request with the same labels
	service.RecordRequest(method, endpoint, status, duration, size)
	
	// Verify the counter was incremented again
	count = testutil.ToFloat64(counter)
	if count != 2 {
		t.Errorf("Expected request counter to be 2, got %f", count)
	}
}

func TestRecordSandboxOperations(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	
	// Create a new metrics service
	service := New()
	
	// Record sandbox creation
	runtime := "python:3.10"
	warmPodUsed := true
	
	service.RecordSandboxCreation(runtime, warmPodUsed)
	
	// Verify the sandbox creation counter was incremented
	counter, err := service.sandboxesCreated.GetMetricWithLabelValues(runtime, "true")
	if err != nil {
		t.Fatalf("Error getting metric: %v", err)
	}
	
	count := testutil.ToFloat64(counter)
	if count != 1 {
		t.Errorf("Expected sandbox creation counter to be 1, got %f", count)
	}
	
	// Record sandbox termination
	service.RecordSandboxTermination(runtime)
	
	// Verify the sandbox termination counter was incremented
	counter, err = service.sandboxesTerminated.GetMetricWithLabelValues(runtime)
	if err != nil {
		t.Fatalf("Error getting metric: %v", err)
	}
	
	count = testutil.ToFloat64(counter)
	if count != 1 {
		t.Errorf("Expected sandbox termination counter to be 1, got %f", count)
	}
}

func TestRecordExecution(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	
	// Create a new metrics service
	service := New()
	
	// Record execution
	execType := "code"
	runtime := "python:3.10"
	status := "success"
	duration := 500 * time.Millisecond
	
	service.RecordExecution(execType, runtime, status, duration)
	
	// Verify the execution counter was incremented
	counter, err := service.executionsTotal.GetMetricWithLabelValues(execType, runtime, status)
	if err != nil {
		t.Fatalf("Error getting metric: %v", err)
	}
	
	count := testutil.ToFloat64(counter)
	if count != 1 {
		t.Errorf("Expected execution counter to be 1, got %f", count)
	}
}

func TestActiveConnections(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	
	// Create a new metrics service
	service := New()
	
	// Increment active connections
	connType := "websocket"
	service.IncrementActiveConnections(connType)
	
	// Verify the active connections gauge was incremented
	gauge, err := service.activeConnections.GetMetricWithLabelValues(connType)
	if err != nil {
		t.Fatalf("Error getting metric: %v", err)
	}
	
	count := testutil.ToFloat64(gauge)
	if count != 1 {
		t.Errorf("Expected active connections to be 1, got %f", count)
	}
	
	// Increment again
	service.IncrementActiveConnections(connType)
	count = testutil.ToFloat64(gauge)
	if count != 2 {
		t.Errorf("Expected active connections to be 2, got %f", count)
	}
	
	// Decrement
	service.DecrementActiveConnections(connType)
	count = testutil.ToFloat64(gauge)
	if count != 1 {
		t.Errorf("Expected active connections to be 1, got %f", count)
	}
	
	// Decrement again
	service.DecrementActiveConnections(connType)
	count = testutil.ToFloat64(gauge)
	if count != 0 {
		t.Errorf("Expected active connections to be 0, got %f", count)
	}
}

func TestUpdateWarmPoolHitRatio(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	
	// Create a new metrics service
	service := New()
	
	// Update warm pool hit ratio
	runtime := "python:3.10"
	ratio := 0.75
	
	service.UpdateWarmPoolHitRatio(runtime, ratio)
	
	// Verify the warm pool hit ratio was set
	gauge, err := service.warmPoolHitRatio.GetMetricWithLabelValues(runtime)
	if err != nil {
		t.Fatalf("Error getting metric: %v", err)
	}
	
	value := testutil.ToFloat64(gauge)
	if value != ratio {
		t.Errorf("Expected warm pool hit ratio to be %f, got %f", ratio, value)
	}
	
	// Update to a new value
	newRatio := 0.85
	service.UpdateWarmPoolHitRatio(runtime, newRatio)
	
	value = testutil.ToFloat64(gauge)
	if value != newRatio {
		t.Errorf("Expected warm pool hit ratio to be %f, got %f", newRatio, value)
	}
}
