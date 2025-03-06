FROM ghcr.io/lenaxia/llmsafespace/base:latest

USER root

# Install Node.js
RUN apt-get update && apt-get install -y --no-install-recommends \
    gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_18.x | bash - \
    && apt-get install -y --no-install-recommends \
    nodejs \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Install base packages
RUN npm install -g \
    express@4.18.2 \
    axios@1.4.0 \
    lodash@4.17.21 \
    d3@7.8.5 \
    chart.js@4.3.0 \
    jest@29.5.0 \
    eslint@8.43.0

# Copy Node.js-specific security configurations
COPY --chown=root:root security/nodejs/restricted_modules.json /etc/llmsafespace/nodejs/

# Copy Node.js security wrapper
COPY --chown=root:root tools/nodejs-security-wrapper.js /opt/llmsafespace/bin/nodejs-security-wrapper.js
RUN chmod +x /opt/llmsafespace/bin/nodejs-security-wrapper.js

USER sandbox
CMD ["nodejs-security-wrapper.js"]
