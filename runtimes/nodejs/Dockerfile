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

# Install base packages including TypeScript support
RUN npm install -g \
    typescript@5.1.6 \
    ts-node@10.9.1 \
    express@4.18.2 \
    axios@1.4.0 \
    lodash@4.17.21 \
    d3@7.8.5 \
    chart.js@4.3.0 \
    jest@29.5.0 \
    ts-jest@29.1.1 \
    @types/node@20.4.2 \
    @types/express@4.17.17 \
    @types/lodash@4.14.195 \
    @types/d3@7.4.0 \
    @types/jest@29.5.3 \
    eslint@8.43.0 \
    @typescript-eslint/parser@6.0.0 \
    @typescript-eslint/eslint-plugin@6.0.0

# Set up TypeScript configuration
COPY --chown=root:root config/tsconfig.json /etc/llmsafespace/nodejs/
ENV TS_NODE_PROJECT=/etc/llmsafespace/nodejs/tsconfig.json

# Copy Node.js-specific security configurations
COPY --chown=root:root security/nodejs/restricted_modules.json /etc/llmsafespace/nodejs/

# Copy Node.js security wrapper
COPY --chown=root:root tools/nodejs-security-wrapper.js /opt/llmsafespace/bin/nodejs-security-wrapper.js
RUN chmod +x /opt/llmsafespace/bin/nodejs-security-wrapper.js

USER sandbox
CMD ["nodejs-security-wrapper.js"]
