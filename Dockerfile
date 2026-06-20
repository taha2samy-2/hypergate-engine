# Stage 1: Build
FROM golang:1.26.4-trixie AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy application source
COPY . .

# Build with optimizations (CGO disabled, Linux OS, stripped symbols)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o hyper-engine ./cmd/engine

# Stage 2: Runtime
FROM debian:12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends netcat-openbsd && \
    rm -rf /var/lib/apt/lists/*

# Copy only the compiled binary
COPY --from=builder /app/hyper-engine /
USER 65532:65532
ENTRYPOINT ["/hyper-engine"]
