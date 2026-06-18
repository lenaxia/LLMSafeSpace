// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"encoding/base64"
	"fmt"
	"strings"
	"text/template"
)

// CloudInitConfig holds the parameters for rendering the relay VM cloud-init.
type CloudInitConfig struct {
	// WgConfig is the rendered wg0.conf content for the relay VM.
	WgConfig string
	// UpstreamURL is the LLM provider endpoint to proxy to.
	UpstreamURL string
	// RouterEndpoint is the WireGuard address relay VMs connect back to.
	RouterEndpoint string
}

const cloudInitTemplate = `#cloud-config
package_update: true
packages:
  - wireguard
  - wireguard-tools
write_files:
  - path: /etc/wireguard/wg0.conf
    content: |
{{ indent 6 .WgConfig }}
    permissions: "0600"
    owner: root:root
  - path: /etc/systemd/system/relay-proxy.service
    content: |
      [Unit]
      Description=LLMSafeSpaces Relay Proxy
      After=network-online.target wg-quick@wg0.service
      Wants=network-online.target
      
      [Service]
      ExecStart=/usr/local/bin/relay-proxy --upstream={{ .UpstreamURL }}
      Restart=always
      RestartSec=5
      User=nobody
      
      [Install]
      WantedBy=multi-user.target
    permissions: "0644"
    owner: root:root
runcmd:
  - systemctl enable wg-quick@wg0
  - systemctl start wg-quick@wg0
  - systemctl enable relay-proxy
  - systemctl start relay-proxy
`

// RenderCloudInit renders the cloud-init userdata for a relay VM.
// The result is base64-encoded as expected by cloud provider APIs.
func RenderCloudInit(cfg CloudInitConfig) (string, error) {
	if cfg.WgConfig == "" {
		return "", fmt.Errorf("WireGuard config is required")
	}
	if cfg.UpstreamURL == "" {
		return "", fmt.Errorf("upstream URL is required")
	}

	tmpl, err := template.New("cloud-init").Funcs(template.FuncMap{
		"indent": indentLines,
	}).Parse(cloudInitTemplate)
	if err != nil {
		return "", fmt.Errorf("parse cloud-init template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("render cloud-init: %w", err)
	}

	return base64.StdEncoding.EncodeToString([]byte(buf.String())), nil
}

// indentLines indents each line of s by n spaces.
func indentLines(n int, s string) string {
	prefix := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}
