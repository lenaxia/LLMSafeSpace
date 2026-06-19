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
# Talos compatibility (#3): the image bundles BOTH the kernel wireguard-tools
# AND the userspace wireguard-go. wg-quick auto-detects: if the kernel WG
# module is available (Talos ships it — verified via Tailscale/NetBird/Nebula
# extensions that all depend on it), it uses the in-kernel implementation; if
# not, wg-quick falls back to wireguard-go via the WG_QUICK_USERSPACE_IMPLEMENTATION
# env (set by render-wg.sh when the kernel module probe fails). This makes the
# image work on Talos, generic Linux, and locked-down kernels alike.
#
#   docker build -f cmd/relay-wireguard/Dockerfile -t llmsafespaces/relay-wireguard:dev .

ARG TARGETARCH=amd64

FROM alpine:3.20 AS base
ARG TARGETARCH
# wireguard-tools provides wg + wg-quick; iproute2 provides ip; jq parses the
# peers ConfigMap; wireguard-go is the userspace fallback; openresolv is a
# wg-quick runtime dep for DNS handling. ca-certificates for TLS if a future
# config fetches peers over HTTPS.
RUN apk add --no-cache \
      wireguard-tools \
      wireguard-go \
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

ENTRYPOINT ["/sbin/tini", "--"]
