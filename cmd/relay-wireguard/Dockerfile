# syntax=docker/dockerfile:1.7
# WireGuard sidecar image for the relay-router (US-42.8 / Epic 42 Layer 2).
#
# Runs render-wg.sh (shipped as a ConfigMap by the Helm chart) to bring up wg0
# (10.42.42.1/24) and hot-sync relay peers. Requires:
#   - wireguard-tools  (wg, wg-quick)
#   - iproute2         (ip)
#   - jq               (parse relay-router-peers ConfigMap JSON)
#   - /dev/net/tun     (mounted via hostPath by the Deployment)
#
# Talos compatibility (#3): the image ships the kernel wireguard-tools. Talos
# (and modern Linux >=5.6) provides the in-kernel WireGuard module, which is
# the path render-wg.sh's detect_wg_impl takes. The userspace wireguard-go
# fallback is NOT bundled here — Alpine 3.20 does not package it (only edge
# community as of 2026-06); detect_wg_impl degrades gracefully (logs the
# absence) on kernels without the module. Operators on locked-down kernels
# that lack the module must build their own image with wireguard-go (e.g. via
# `go install golang.zx2k4x.net/wireguard/cmd/wireguard-go@latest`).
#
#   docker build -f cmd/relay-wireguard/Dockerfile -t llmsafespaces/relay-wireguard:dev .

FROM alpine:3.20 AS base
# wireguard-tools provides wg + wg-quick; iproute2 provides ip; jq parses the
# peers ConfigMap; openresolv is a wg-quick runtime dep for DNS handling.
# ca-certificates for TLS if a future config fetches peers over HTTPS.
#
# Talos target note: this image ships the kernel wireguard-tools. Talos (and
# modern Linux ≥5.6) provides the WireGuard kernel module, which is the path
# detect_wg_impl in render-wg.sh takes. The userspace wireguard-go fallback is
# NOT bundled because Alpine 3.20 does not package it (it's only in edge
# community as of 2026-06); detect_wg_impl degrades gracefully (logs the
# absence) on kernels without the module. Operators on locked-down kernels
# that lack the module must build their own image with wireguard-go (e.g. via
# `go install golang.zx2k4x.net/wireguard/cmd/wireguard-go@latest`).
RUN apk add --no-cache \
      wireguard-tools \
      iproute2 \
      jq \
      openresolv \
      ca-certificates \
      tini \
 && addgroup -S wg \
 && adduser  -S -G wg -h /etc/wireguard wg

# render-wg.sh is shipped by the Helm chart as the relay-router-wg-scripts
# ConfigMap and mounted at /scripts. The chart's command: [/scripts/render-wg.sh]
# overrides this default CMD, but keep a sane fallback for ad-hoc runs.
CMD ["wg"]

# Default to the non-root wg user. The Helm chart overrides to runAsUser: 0 at
# runtime because netlink/tun operations require root, but the image ships a
# non-root default per DS-0002 (container hardening best practice). The chart
# declares this explicitly in the sidecar's securityContext.
USER wg

ENTRYPOINT ["/sbin/tini", "--"]
