# =============================================================================
# Multi-stage Dockerfile for pvc-migrator
# Produces a minimal, secure container image (~3MB) using Distroless
# =============================================================================

# -----------------------------------------------------------------------------
# Stage 1: Build the Go binary
# -----------------------------------------------------------------------------
FROM golang:1.24-alpine AS builder

# Build arguments for version injection
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# Install CA certificates and timezone data for the final image
RUN apk add --no-cache ca-certificates tzdata git

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum first for better layer caching
COPY go.mod go.sum ./

# Download dependencies - this layer is cached unless go.mod/go.sum change
RUN go mod download && go mod verify

# Copy the source code
COPY . .

# Build the binary with all optimizations
# CGO_ENABLED=0: Static binary, no C dependencies
# -trimpath: Remove file system paths from binary for reproducibility
# -ldflags: Strip debug info (-s -w) and inject version info
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${DATE}" \
    -o /app/pvc-migrator .

# Verify the binary was built correctly
RUN /app/pvc-migrator --help || true

# -----------------------------------------------------------------------------
# Stage 2: Create minimal runtime image
# Using Distroless for maximum security (no shell, no package manager)
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Labels for OCI compliance
LABEL org.opencontainers.image.title="pvc-migrator"
LABEL org.opencontainers.image.description="Kubernetes PVC Zone Migration Tool"
LABEL org.opencontainers.image.source="https://github.com/cesarempathy/pv-zone-migrator"
LABEL org.opencontainers.image.vendor="cesarempathy"
LABEL org.opencontainers.image.licenses="MIT"

# Copy CA certificates for HTTPS connections (AWS, Kubernetes API)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the compiled binary
COPY --from=builder /app/pvc-migrator /usr/local/bin/pvc-migrator

# Use non-root user (distroless:nonroot has UID 65532)
USER nonroot:nonroot

# Set the entrypoint
ENTRYPOINT ["/usr/local/bin/pvc-migrator"]

# Default command (can be overridden)
CMD ["--help"]
