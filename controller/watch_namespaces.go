package main

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// parseWatchNamespaces converts the --watch-namespaces flag value into a
// cache.Options.DefaultNamespaces map.
//
// Behavior:
//   - empty string         → nil  (watch all namespaces — controller-runtime default)
//   - "*"                  → nil  (watch all namespaces — explicit form)
//   - "ns1"                → {"ns1": {}}
//   - "ns1,ns2,ns3"        → {"ns1": {}, "ns2": {}, "ns3": {}}
//
// Whitespace around commas is trimmed. Empty entries (e.g. "ns1,,ns2") are
// ignored. Duplicates collapse silently.
func parseWatchNamespaces(s string) map[string]cache.Config {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return nil
	}

	out := map[string]cache.Config{}
	for _, ns := range strings.Split(s, ",") {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		out[ns] = cache.Config{}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}
