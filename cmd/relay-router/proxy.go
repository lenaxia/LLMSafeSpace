// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	fallbackHeader      = "X-Relay-Status"
	fallbackHeaderValue = "fallback"
)

var routerHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailers":            {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"X-Workspace-Id":      {},
}

// routerProxy is the HTTP handler that selects a relay and forwards
// the request. If no relays are healthy, it enters fallback mode.
type routerProxy struct {
	fleet      *relayFleet
	detector   *detector429
	metrics    *routerMetrics
	httpClient *http.Client
	relayPort  int

	fallback       *fallbackProxy
	fallbackActive bool
	fallbackMu     sync.RWMutex
}

func newRouterProxy(fleet *relayFleet, detector *detector429, metrics *routerMetrics, relayPort int, fallback *fallbackProxy) *routerProxy {
	return &routerProxy{
		fleet:      fleet,
		detector:   detector,
		metrics:    metrics,
		httpClient: &http.Client{Timeout: 0},
		relayPort:  relayPort,
		fallback:   fallback,
	}
}

func (rp *routerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	relayID, wgIP, ok := rp.fleet.SelectRelay()

	if !ok {
		rp.handleFallback(w, r)
		return
	}

	rp.setFallbackActive(false)
	rp.forwardToRelay(w, r, relayID, wgIP)
}

func (rp *routerProxy) forwardToRelay(w http.ResponseWriter, r *http.Request, relayID, wgIP string) {
	target := fmt.Sprintf("http://%s:%d%s", wgIP, rp.relayPort, r.URL.Path)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	upstreamReq.ContentLength = r.ContentLength
	copyRouterHeaders(upstreamReq.Header, r.Header)

	rp.fleet.RecordStreamStart(relayID)
	defer rp.fleet.RecordStreamEnd(relayID)

	resp, err := rp.httpClient.Do(upstreamReq)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		rp.fleet.RecordHealthCheck(relayID, false)
		rp.metrics.recordRequest(relayID, http.StatusBadGateway)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyRouterHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	var egress int64
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written, _ := w.Write(buf[:n])
			egress += int64(written)
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}

	rp.fleet.RecordRequest(relayID, resp.StatusCode)
	rp.fleet.RecordEgress(relayID, egress)
	rp.metrics.recordRequest(relayID, resp.StatusCode)
	rp.detector.OnResponse(relayID, resp.StatusCode)
}

func (rp *routerProxy) handleFallback(w http.ResponseWriter, r *http.Request) {
	if rp.fallback == nil {
		http.Error(w, "no relay available", http.StatusBadGateway)
		return
	}

	rp.setFallbackActive(true)
	rp.fallback.ServeHTTP(w, r)
}

func (rp *routerProxy) setFallbackActive(active bool) {
	rp.fallbackMu.Lock()
	defer rp.fallbackMu.Unlock()
	if rp.fallbackActive != active {
		rp.fallbackActive = active
		rp.metrics.setFallbackActive(active)
	}
}

// fallbackProxy implements rate-limited direct routing to the upstream
// when all relay VMs are unhealthy. Per the design:
// - Global rate limit of 1 req/2s (token bucket)
// - Max 1 concurrent in-flight request
// - X-Relay-Status: fallback header on all responses
// - Requests exceeding limits get 429 + Retry-After: 2
type fallbackProxy struct {
	upstreamURL   string
	httpClient    *http.Client
	rateInterval  time.Duration
	maxConcurrent int
	mu            sync.Mutex
	lastRequest   time.Time
	inFlight      int
}

func newFallbackProxy(upstreamURL string, rate float64, maxConcurrent int) (*fallbackProxy, error) {
	if upstreamURL == "" {
		return nil, fmt.Errorf("upstream URL is required")
	}
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid scheme: %s", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host in upstream URL")
	}

	interval := time.Duration(0)
	if rate > 0 {
		interval = time.Duration(float64(time.Second) / rate)
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	return &fallbackProxy{
		upstreamURL:   upstreamURL,
		httpClient:    &http.Client{Timeout: 0},
		rateInterval:  interval,
		maxConcurrent: maxConcurrent,
	}, nil
}

func (fp *fallbackProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fp.mu.Lock()

	if fp.inFlight >= fp.maxConcurrent {
		fp.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	now := time.Now()
	if fp.rateInterval > 0 && now.Sub(fp.lastRequest) < fp.rateInterval {
		fp.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	fp.lastRequest = now
	fp.inFlight++
	fp.mu.Unlock()

	defer func() {
		fp.mu.Lock()
		fp.inFlight--
		fp.mu.Unlock()
	}()

	fp.forward(w, r)
}

func (fp *fallbackProxy) forward(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSuffix(fp.upstreamURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	upstreamReq.ContentLength = r.ContentLength
	copyRouterHeaders(upstreamReq.Header, r.Header)

	resp, err := fp.httpClient.Do(upstreamReq)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyRouterHeaders(w.Header(), resp.Header)
	w.Header().Set(fallbackHeader, fallbackHeaderValue)
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
}

func copyRouterHeaders(dst, src http.Header) {
	for key, values := range src {
		if _, isHop := routerHopHeaders[http.CanonicalHeaderKey(key)]; isHop {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func defaultRouterTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
	}
}
