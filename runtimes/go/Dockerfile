FROM llmsafespace/base:latest

USER root

# Install Go
ENV GO_VERSION=1.20.5
RUN curl -sSL https://golang.org/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"
ENV PATH="${GOPATH}/bin:${PATH}"

# Create Go directories
RUN mkdir -p ${GOPATH}/src ${GOPATH}/bin && \
    chmod -R 777 ${GOPATH}

# Install common Go packages
RUN go install github.com/gorilla/mux@v1.8.0 && \
    go install github.com/gin-gonic/gin@v1.9.1 && \
    go install github.com/spf13/cobra@v1.7.0 && \
    go install github.com/stretchr/testify@v1.8.4 && \
    go install gonum.org/v1/gonum@v0.13.0

# Copy Go-specific security configurations
COPY --chown=root:root security/go/restricted_packages.json /etc/llmsafespace/go/

# Copy Go security wrapper
COPY --chown=root:root tools/go-security-wrapper.go /opt/llmsafespace/bin/go-security-wrapper.go
RUN go build -o /opt/llmsafespace/bin/go-security-wrapper /opt/llmsafespace/bin/go-security-wrapper.go && \
    chmod +x /opt/llmsafespace/bin/go-security-wrapper

USER sandbox
CMD ["/opt/llmsafespace/bin/go-security-wrapper"]
