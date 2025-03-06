FROM ghcr.io/lenaxia/llmsafespace/python:latest

# Add metadata labels
LABEL org.opencontainers.image.title="LLMSafeSpace Python ML Runtime"
LABEL org.opencontainers.image.description="Python runtime with ML packages for LLMSafeSpace"
LABEL org.opencontainers.image.source="https://github.com/lenaxia/llmsafespace"

# Version arguments
ARG PANDAS_VERSION=2.0.2
ARG MATPLOTLIB_VERSION=3.7.1
ARG SCIKIT_LEARN_VERSION=1.2.2
ARG TENSORFLOW_VERSION=2.12.0
ARG PYTORCH_VERSION=2.0.1

USER root

# Install all ML packages in a single layer
RUN pip install --no-cache-dir --timeout 300 --retries 5 \
        pandas==${PANDAS_VERSION} \
        matplotlib==${MATPLOTLIB_VERSION} \
        scikit-learn==${SCIKIT_LEARN_VERSION} \
        tensorflow==${TENSORFLOW_VERSION} && \
    pip install --no-cache-dir --timeout 300 --retries 5 \
        torch==${PYTORCH_VERSION}+cpu --index-url https://download.pytorch.org/whl/cpu && \
    # Verify installations
    python3 -c "import pandas; import matplotlib; import sklearn; import tensorflow; import torch" && \
    # Clean pip cache
    rm -rf /root/.cache/pip/*

USER sandbox
WORKDIR /workspace
CMD ["/opt/llmsafespace/bin/python-security-wrapper.py"]
