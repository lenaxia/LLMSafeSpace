FROM ghcr.io/lenaxia/llmsafespace/python:latest

USER root

# Install ML packages
RUN pip install --no-cache-dir --timeout 300 --retries 5 \
    pandas==2.0.2 \
    matplotlib==3.7.1 \
    scikit-learn==1.2.2 \
    tensorflow==2.12.0

# Install PyTorch separately to use its index
RUN pip install --no-cache-dir --timeout 300 --retries 5 \
    torch==2.0.1+cpu --index-url https://download.pytorch.org/whl/cpu

USER sandbox
CMD ["/opt/llmsafespace/bin/python-security-wrapper.py"]
