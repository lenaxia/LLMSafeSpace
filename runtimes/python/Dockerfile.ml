FROM ghcr.io/lenaxia/llmsafespace/python:latest

# Add metadata labels
LABEL org.opencontainers.image.title="LLMSafeSpace Python ML Runtime"
LABEL org.opencontainers.image.description="Python runtime with ML packages for LLMSafeSpace"
LABEL org.opencontainers.image.source="https://github.com/lenaxia/llmsafespace"
LABEL org.opencontainers.image.version="1.0.0"

# Version arguments
ARG NUMPY_VERSION=1.24.3
ARG PANDAS_VERSION=2.0.2
ARG MATPLOTLIB_VERSION=3.7.1
ARG SCIKIT_LEARN_VERSION=1.2.2
ARG TENSORFLOW_VERSION=2.12.0
ARG PYTORCH_VERSION=2.0.1

USER root

# Install core numerical packages first
RUN pip install --no-cache-dir --timeout 300 --retries 5 \
        numpy==${NUMPY_VERSION} \
        pandas==${PANDAS_VERSION} && \
    # Verify core installations
    python3 -c "import numpy; import pandas; print('Core packages installed successfully')"

# Install ML and visualization packages
RUN pip install --no-cache-dir --timeout 300 --retries 5 \
        matplotlib==${MATPLOTLIB_VERSION} \
        scikit-learn==${SCIKIT_LEARN_VERSION} \
        tensorflow==${TENSORFLOW_VERSION} && \
    # Install PyTorch separately due to custom index
    pip install --no-cache-dir --timeout 300 --retries 5 \
        torch==${PYTORCH_VERSION}+cpu --index-url https://download.pytorch.org/whl/cpu && \
    # Verify all installations
    python3 -c "import numpy; import pandas; import matplotlib; import sklearn; import tensorflow; import torch; print('All packages verified')" && \
    # Clean pip cache
    rm -rf /root/.cache/pip/*

# Set environment variables for better ML package behavior
ENV PYTHONUNBUFFERED=1
ENV MPLBACKEND=Agg
ENV TF_CPP_MIN_LOG_LEVEL=2

USER sandbox
WORKDIR /workspace
CMD ["/opt/llmsafespace/bin/python-security-wrapper.py"]
