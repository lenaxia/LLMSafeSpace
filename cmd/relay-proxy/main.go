// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	defaultUpstreamURL       = "https://ai.thekao.cloud/v1"
	defaultListenAddr        = "10.42.42.2:8080"
	defaultKeepaliveInterval = 30 * time.Second
)

type config struct {
	upstreamURL       string
	listenAddr        string
	keepaliveInterval time.Duration
}

func loadConfig() config {
	return config{
		upstreamURL:       getEnv("UPSTREAM_URL", defaultUpstreamURL),
		listenAddr:        getEnv("LISTEN_ADDR", defaultListenAddr),
		keepaliveInterval: getEnvDuration("KEEPALIVE_INTERVAL", defaultKeepaliveInterval),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
		},
	}
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func metricsHandler(metrics *relayMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.writePrometheus(w)
	}
}

func main() {
	cfg := loadConfig()

	client := defaultHTTPClient()
	metrics := newRelayMetrics()

	proxy, err := newProxyHandler(cfg.upstreamURL, client, metrics)
	if err != nil {
		log.Fatalf("relay-proxy: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/metrics", metricsHandler(metrics))
	mux.Handle("/", proxy)

	ka := newKeepalive(cfg.upstreamURL, client, cfg.keepaliveInterval, metrics)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ka.run(ctx)

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("relay-proxy: shutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		cancel()
	}()

	log.Printf("relay-proxy: listening on %s, upstream=%s", cfg.listenAddr, cfg.upstreamURL)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("relay-proxy: %v", err)
	}
	log.Println("relay-proxy: stopped")
}
