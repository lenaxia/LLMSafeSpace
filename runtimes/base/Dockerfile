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
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create sandbox user and group
RUN groupadd -g 1000 sandbox && \
    useradd -u 1000 -g sandbox -s /bin/bash -m -d /home/sandbox sandbox

# Create directory structure
RUN mkdir -p /workspace /opt/llmsafespace /etc/llmsafespace && \
    chown -R sandbox:sandbox /workspace && \
    chmod 755 /workspace

# Copy security configuration files
COPY --chown=root:root security/seccomp-profiles/ /etc/llmsafespace/seccomp/
COPY --chown=root:root security/apparmor-profiles/ /etc/llmsafespace/apparmor/

# Copy monitoring tools
COPY --chown=root:root tools/sandbox-monitor /opt/llmsafespace/bin/sandbox-monitor
COPY --chown=root:root tools/execution-tracker /opt/llmsafespace/bin/execution-tracker

# Make tools executable
RUN chmod +x /opt/llmsafespace/bin/*

# Security hardening
RUN find / -perm /6000 -type f -exec chmod a-s {} \; || true

# Set working directory
WORKDIR /workspace

# Use tini as init
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/bin/bash"]

# Switch to sandbox user
USER sandbox:sandbox
