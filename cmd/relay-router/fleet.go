// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	relayProviderOCI = "oci"
	relayProviderGCP = "gcp"

	relayStateHealthy   = "healthy"
	relayStateDraining  = "draining"
	relayStateUnhealthy = "unhealthy"
	relayStateSuspect   = "suspect"

	wsIDHeader = "X-Workspace-ID"
)

// PeerConfig is the JSON shape of the relay-router-peers ConfigMap.
type PeerConfig struct {
	Relays []PeerEntry `json:"relays"`
}

// PeerEntry represents one relay VM in the ConfigMap.
type PeerEntry struct {
	ID       string `json:"id"`
	WgIP     string `json:"wgIP"`
	Provider string `json:"provider"`
	State    string `json:"state"`
}

// ParsePeerConfig decodes the ConfigMap JSON into a PeerConfig.
func ParsePeerConfig(data []byte) (PeerConfig, error) {
	var cfg PeerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return PeerConfig{}, fmt.Errorf("parse peer config: %w", err)
	}
	return cfg, nil
}

// relayHealth tracks the router's independent health view of a relay.
type relayHealth struct {
	consecutiveFailures int
	lastCheckAt         time.Time
	lastSuccessAt       time.Time
}

// relay429State tracks 429 detection state per relay.
type relay429State struct {
	window            []time.Time
	consecutiveProbes int
	lastProbeAt       time.Time
	markedDraining    bool
	drainingReason    string
	drainingSince     time.Time
}

// relayEntry is the router's internal state for a single relay VM.
type relayEntry struct {
	peer   PeerEntry
	health relayHealth
	s429   relay429State

	totalRequests int64
	requests429   int64
	activeStreams int64
	egressBytes   int64
}

// relayFleet is the thread-safe central state of all relay VMs.
// The selector, health checker, 429 detector, and proxy handler all
// read and write through this struct.
type relayFleet struct {
	mu           sync.RWMutex
	relays       map[string]*relayEntry
	unhealthyThr int
	window       time.Duration
	rng          *rand.Rand
}

// newRelayFleet creates a fleet with the given health check unhealthy
// threshold and 429 detection window.
func newRelayFleet(unhealthyThreshold int, window time.Duration) *relayFleet {
	return &relayFleet{
		relays:       make(map[string]*relayEntry),
		unhealthyThr: unhealthyThreshold,
		window:       window,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// UpdatePeers replaces the peer list from the ConfigMap. Existing health
// and 429 state for relays still present is preserved. Relays no longer
// in the ConfigMap are removed. New relays start with empty health state.
func (f *relayFleet) UpdatePeers(peers []PeerEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()

	newSet := make(map[string]bool, len(peers))
	for _, p := range peers {
		newSet[p.ID] = true
		if existing, ok := f.relays[p.ID]; ok {
			existing.peer = p
		} else {
			f.relays[p.ID] = &relayEntry{peer: p}
		}
	}

	for id := range f.relays {
		if !newSet[id] {
			delete(f.relays, id)
		}
	}
}

// SelectRelay picks a relay using weighted random selection.
// OCI receives 100% of traffic when healthy. GCP receives traffic only
// when OCI is unavailable. Returns the selected relay ID and WG IP,
// or empty strings if no healthy relay is available.
func (f *relayFleet) SelectRelay() (id, wgIP string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	eligible := f.eligibleRelaysLocked()
	if len(eligible) == 0 {
		return "", "", false
	}

	hasOCI := false
	for _, e := range eligible {
		if e.peer.Provider == relayProviderOCI {
			hasOCI = true
			break
		}
	}

	type candidate struct {
		entry  *relayEntry
		weight float64
	}

	var total float64
	candidates := make([]candidate, 0, len(eligible))
	for _, e := range eligible {
		w := relayWeight(e.peer.Provider, e.peer.State, f.healthStateLocked(e))
		if hasOCI && e.peer.Provider == relayProviderGCP {
			w = 0
		}
		if w > 0 {
			candidates = append(candidates, candidate{entry: e, weight: w})
			total += w
		}
	}
	if total == 0 || len(candidates) == 0 {
		return "", "", false
	}

	r := f.rng.Float64() * total
	cumulative := 0.0
	for _, c := range candidates {
		cumulative += c.weight
		if r <= cumulative {
			return c.entry.peer.ID, c.entry.peer.WgIP, true
		}
	}

	last := candidates[len(candidates)-1]
	return last.entry.peer.ID, last.entry.peer.WgIP, true
}

// eligibleRelaysLocked returns relays that can receive new traffic.
// Draining relays are excluded. Must be called with mu held.
func (f *relayFleet) eligibleRelaysLocked() []*relayEntry {
	result := make([]*relayEntry, 0, len(f.relays))
	for _, e := range f.relays {
		if e.peer.State == relayStateDraining {
			continue
		}
		if f.healthStateLocked(e) == relayStateUnhealthy {
			continue
		}
		if f.is429DrainingLocked(e) {
			continue
		}
		result = append(result, e)
	}
	return result
}

// healthStateLocked returns the effective health state of a relay,
// combining the ConfigMap state with the router's independent health checks.
func (f *relayFleet) healthStateLocked(e *relayEntry) string {
	if e.health.consecutiveFailures >= f.unhealthyThr {
		return relayStateUnhealthy
	}
	return e.peer.State
}

// is429DrainingLocked returns true if the 429 detector has marked this
// relay as draining due to 429 storm or consecutive probe failures.
func (f *relayFleet) is429DrainingLocked(e *relayEntry) bool {
	return e.s429.markedDraining
}

// relayWeight assigns traffic weights by provider and health state.
// OCI primary (weight 100), GCP failover (weight 1). Suspect relays
// get reduced weight. This encodes Design Principle 4 (OCI-primary).
func relayWeight(provider, peerState, healthState string) float64 {
	if healthState == relayStateUnhealthy {
		return 0
	}
	w := 1.0
	if provider == relayProviderOCI {
		w = 100
	}
	if peerState == relayStateSuspect || healthState == relayStateSuspect {
		w *= 0.1
	}
	return w
}

// RecordRequest records a proxied request result for a relay.
func (f *relayFleet) RecordRequest(relayID string, statusCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, ok := f.relays[relayID]
	if !ok {
		return
	}
	e.totalRequests++
	if statusCode == 429 {
		e.requests429++
		e.s429.window = append(e.s429.window, time.Now())
		f.prune429WindowLocked(e)
	}
}

// RecordStreamStart increments the active stream counter.
func (f *relayFleet) RecordStreamStart(relayID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.relays[relayID]; ok {
		e.activeStreams++
	}
}

// RecordStreamEnd decrements the active stream counter.
func (f *relayFleet) RecordStreamEnd(relayID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.relays[relayID]; ok {
		e.activeStreams--
		if e.activeStreams < 0 {
			e.activeStreams = 0
		}
	}
}

// RecordEgress adds egress bytes for quota tracking.
func (f *relayFleet) RecordEgress(relayID string, bytes int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.relays[relayID]; ok {
		e.egressBytes += bytes
	}
}

// RecordHealthCheck updates the health state for a relay.
func (f *relayFleet) RecordHealthCheck(relayID string, success bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, ok := f.relays[relayID]
	if !ok {
		return
	}
	now := time.Now()
	e.health.lastCheckAt = now
	if success {
		e.health.consecutiveFailures = 0
		e.health.lastSuccessAt = now
	} else {
		e.health.consecutiveFailures++
	}
}

// Mark429Draining marks a relay as draining due to 429 storm.
func (f *relayFleet) Mark429Draining(relayID, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.relays[relayID]; ok {
		e.s429.markedDraining = true
		e.s429.drainingReason = reason
		e.s429.drainingSince = time.Now()
	}
}

// Mark429Suspect records that the immediate probe returned 429.
func (f *relayFleet) Mark429Suspect(relayID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.relays[relayID]; ok {
		e.s429.consecutiveProbes++
		e.s429.lastProbeAt = time.Now()
	}
}

// Clear429State resets all 429 detection state for a relay, including
// the draining flag. Called when a probe succeeds or the relay recovers
// (e.g. after controller rotation). Without this, a relay marked as
// 429-draining would be permanently excluded from selection.
func (f *relayFleet) Clear429State(relayID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.relays[relayID]; ok {
		e.s429.consecutiveProbes = 0
		e.s429.markedDraining = false
		e.s429.drainingReason = ""
		e.s429.drainingSince = time.Time{}
		e.s429.window = nil
	}
}

// prune429WindowLocked removes 429 timestamps outside the detection window.
// Must be called with mu held (write lock).
func (f *relayFleet) prune429WindowLocked(e *relayEntry) {
	cutoff := time.Now().Add(-f.window)
	filtered := e.s429.window[:0]
	for _, t := range e.s429.window {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	e.s429.window = filtered
}

// windowed429CountLocked returns the count of 429s within the rolling
// detection window. Must be called with mu held.
func (f *relayFleet) windowed429CountLocked(e *relayEntry) int {
	cutoff := time.Now().Add(-f.window)
	count := 0
	for _, t := range e.s429.window {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// windowedRequestCountLocked returns total requests in the rolling window.
// Since we don't track per-request timestamps for non-429 requests, this
// uses the lifetime total as an upper bound. The storm detection compares
// windowed 429 count against a threshold, not a rate, so this approximation
// is conservative (tends to undercount the rate).
func (f *relayFleet) windowedRequestCountLocked(e *relayEntry) int64 {
	return e.totalRequests
}

// HealthyRelays returns relay IDs and their health status.
func (f *relayFleet) HealthyRelays() []RelayStatus {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([]RelayStatus, 0, len(f.relays))
	for _, e := range f.relays {
		result = append(result, RelayStatus{
			ID:            e.peer.ID,
			WgIP:          e.peer.WgIP,
			Provider:      e.peer.Provider,
			Healthy:       f.healthStateLocked(e) == relayStateHealthy,
			ActiveStreams: e.activeStreams,
			EgressBytes:   e.egressBytes,
			State:         e.peer.State,
			Requests429:   e.requests429,
			TotalRequests: e.totalRequests,
			Draining429:   e.s429.markedDraining,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// HasHealthyRelay returns true if at least one relay is routable.
func (f *relayFleet) HasHealthyRelay() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.eligibleRelaysLocked()) > 0
}

// ActiveStreams returns the active stream count for a specific relay.
func (f *relayFleet) ActiveStreams(relayID string) int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if e, ok := f.relays[relayID]; ok {
		return e.activeStreams
	}
	return 0
}

// RelayStatus is a read-only snapshot of a relay's state for metrics.
type RelayStatus struct {
	ID            string
	WgIP          string
	Provider      string
	Healthy       bool
	ActiveStreams int64
	EgressBytes   int64
	State         string
	Requests429   int64
	TotalRequests int64
	Draining429   bool
}

// GetWgIP returns the WG IP for a relay ID, or empty if not found.
func (f *relayFleet) GetWgIP(relayID string) string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if e, ok := f.relays[relayID]; ok {
		return e.peer.WgIP
	}
	return ""
}

// Relay429Rate returns the windowed 429 rate (429s in window / total requests)
// and consecutive probe count for a relay. Used by the 429 detector for
// storm detection. Acquires a write lock because pruning mutates the window.
func (f *relayFleet) Relay429Rate(relayID string) (rate float64, consecutiveProbes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.relays[relayID]
	if !ok {
		return 0, 0
	}
	f.prune429WindowLocked(e)
	windowed429 := len(e.s429.window)
	total := f.windowedRequestCountLocked(e)
	if total > 0 {
		rate = float64(windowed429) / float64(total)
	}
	return rate, e.s429.consecutiveProbes
}

// String returns a human-readable representation of the fleet state.
func (f *relayFleet) String() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ids := make([]string, 0, len(f.relays))
	for id := range f.relays {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return fmt.Sprintf("relayFleet{relays: [%s]}", strings.Join(ids, ", "))
}
