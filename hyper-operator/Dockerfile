# ========================================================
# Stage 1: Build using Debian with your custom Go version
# ========================================================
# Back to your exact Go toolchain version (1.26.4-trixie)
FROM golang:1.26.4-trixie AS builder

# Enable Go to automatically download and switch to the required toolchain if needed
ENV GOTOOLCHAIN=auto

WORKDIR /app

# Copy dependency files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire monorepo context
COPY . .

# Compile the highly-optimized static Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o manager ./hyper-operator/cmd/main.go

# ========================================================
# Stage 2: Minimal Debian 12 (Bookworm-slim) Runtime
# ========================================================
FROM debian:bookworm-slim

# Install CA certificates for secure HTTPS/gRPC communication with K8s API
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Create a secure non-root user and group in Debian for K8s compliance
RUN groupadd -g 10001 appgroup && \
    useradd -u 10001 -g appgroup -m -s /bin/bash appuser

WORKDIR /

# Copy the compiled operator binary from the builder stage
COPY --from=builder /app/manager .

# Run the container as the non-privileged appuser we created
USER appuser:appgroup

ENTRYPOINT ["/manager"]