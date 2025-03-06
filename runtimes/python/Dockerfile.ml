FROM ghcr.io/lenaxia/llmsafespace/python:latest

USER root

# Install ML packages
RUN pip install --no-cache-dir \
    numpy==1.24.3 \
    pandas==2.0.2 \
    matplotlib==3.7.1 \
    scikit-learn==1.2.2

# Install deep learning packages (CPU versions)
RUN pip install --no-cache-dir --timeout 300 --retries 5 tensorflow==2.12.0 && \
    pip install --no-cache-dir --timeout 300 --retries 5 torch==2.0.1+cpu --index-url https://download.pytorch.org/whl/cpu

USER sandbox
CMD ["python-security-wrapper.py"]
