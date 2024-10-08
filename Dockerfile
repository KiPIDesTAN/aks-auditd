ARG BUILDKIT_SBOM_SCAN_CONTEXT=true
ARG BUILDKIT_SBOM_SCAN_STAGE=base,final
FROM mcr.microsoft.com/azurelinux/base/core:3.0 AS base

WORKDIR /app

# Copy source code
COPY src/*.go ./
COPY scripts/get_golang.sh ./

# Copy example config file
RUN mkdir -p /etc/aks-auditd
COPY config.yaml /etc/aks-auditd/config.yaml

# Update tdnf and install requirements for updating golang
RUN tdnf update \
    && tdnf install -y ca-certificates tar

# Install a specific Go version to get around any security issues not in the Azure Linux golang package
ENV GO_BASE_PATH="/tmp" PATH="/tmp/go/bin:${PATH}"
RUN chmod +x get_golang.sh && ./get_golang.sh

# Compile the binary
RUN go mod init aksauditd \
    && go mod tidy \
    && GOARCH=amd64 CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o aks-auditd main.go

FROM mcr.microsoft.com/azurelinux/distroless/minimal:3.0 AS final

LABEL org.opencontainers.image.source=https://github.com/KiPIDesTAN/aks-auditd
LABEL org.opencontainers.image.description="Auditd for Azure Kubernetes Service"
LABEL org.opencontainers.image.licenses=GPLv3

WORKDIR /app

COPY --from=base /app/aks-auditd .
COPY --from=base /etc/aks-auditd /etc/aks-auditd

VOLUME /node
VOLUME /auditd-rules
VOLUME /audisp-plugins

ENTRYPOINT ["/app/aks-auditd"]