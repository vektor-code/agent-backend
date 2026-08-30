# Self-contained build: the build context is THIS directory. The shared code
# lives in ./shared and is wired in via `replace github.com/kubetrace/shared => ./shared`,
# so no sibling module is needed:
#   docker build -f Dockerfile .
#
# This image is the in-cluster collector + controller only. Language agents
# injected into application pods are built from Dockerfile.agent-*.
#
# --- Stage 1: Build ---
FROM golang:1.25-alpine AS builder

# Module mode, not workspace mode.
ENV GOWORK=off

WORKDIR /src

# Dependency manifests first, so the module cache layer survives source edits.
COPY shared/go.mod ./shared/
COPY go.mod go.sum ./
RUN go mod download

# Source code (service + vendored shared package)
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /out/agent ./cmd/agent-backend

# --- Stage 2: Final image ---
FROM alpine:3.19

# Install CA certificates for secure connections
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Run as non-root user for security
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

COPY --from=builder /out/agent .

USER appuser

EXPOSE 4317 4318

ENTRYPOINT ["./agent"]
