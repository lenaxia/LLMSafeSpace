FROM ghcr.io/lenaxia/llmsafespace/base:latest

USER root

# Install Python and dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3.9 \
    python3.9-dev \
    python3-pip \
    python3-venv \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Create and activate virtual environment
RUN python3.9 -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Install base packages
RUN pip install --no-cache-dir --upgrade pip setuptools wheel && \
    pip install --no-cache-dir \
    requests==2.31.0 \
    ipython==8.14.0 \
    pytest==7.3.1 \
    numpy==1.24.3  # Required by many Python packages

# Copy Python-specific security configurations
COPY --chown=root:root security/python/restricted_modules.json /etc/llmsafespace/python/
COPY --chown=root:root security/python/sitecustomize.py /opt/venv/lib/python3.9/site-packages/

# Copy Python security wrapper
COPY --chown=root:root tools/python-security-wrapper.py /opt/llmsafespace/bin/python-security-wrapper.py
RUN chmod +x /opt/llmsafespace/bin/python-security-wrapper.py

USER sandbox
CMD ["python-security-wrapper.py"]
