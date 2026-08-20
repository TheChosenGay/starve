package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsHandlerRoutes(t *testing.T) {
	metricsCalled := false
	handler := newMetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		metricsCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("healthz = %d %q", health.Code, health.Body.String())
	}

	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusNoContent || !metricsCalled {
		t.Fatalf("metrics route = %d, called=%v", metrics.Code, metricsCalled)
	}
}
