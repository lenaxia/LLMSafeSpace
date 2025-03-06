FROM debian:bullseye-slim

# Set non-interactive mode for apt
ENV DEBIAN_FRONTEND=noninteractive

# Install core utilities and security tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    jq \
    procps \
    tini \
    libseccomp2 \
    libseccomp-dev \
    apparmor \
    bc \
    iproute2 \
    iputils-ping \
    stress \
    lsof \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create sandbox user and group
RUN groupadd -g 1000 sandbox && \
    useradd -u 1000 -g sandbox -s /bin/bash -m -d /home/sandbox sandbox

# Create directory structure
RUN mkdir -p /workspace /opt/llmsafespace/{bin,lib} /etc/llmsafespace /var/log/llmsafespace && \
    chown -R sandbox:sandbox /workspace /var/log/llmsafespace && \
    chmod 755 /workspace && \
    chmod 1777 /tmp

# Copy security configuration files
COPY --chown=root:root security/seccomp-profiles/ /etc/llmsafespace/seccomp/
COPY --chown=root:root security/apparmor-profiles/ /etc/llmsafespace/apparmor/

# Copy tools and scripts
COPY --chown=root:root tools/sandbox-monitor /opt/llmsafespace/bin/
COPY --chown=root:root tools/execution-tracker /opt/llmsafespace/bin/
COPY --chown=root:root tools/health-check /opt/llmsafespace/bin/
COPY --chown=root:root tools/cleanup-pod /opt/llmsafespace/bin/

# Make tools executable
RUN chmod +x /opt/llmsafespace/bin/*

# Security hardening
RUN find / -perm /6000 -type f -exec chmod a-s {} \; || true

# Add health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/opt/llmsafespace/bin/health-check"]

# Set working directory
WORKDIR /workspace

# Use tini as init
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/bin/bash"]

# Switch to sandbox user
USER sandbox:sandbox
