#!/usr/bin/env bash
# Build CRNET instrumentation-nginx (fat nginx/apache webserver agent).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
AGENT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

REGISTRY="${REGISTRY:-ghcr.io}"
IMAGE_NS="${IMAGE_NS:-crnet-apm}"
TAG="${TAG:-prod}"
PLATFORM="${PLATFORM:-linux/amd64}"
PUSH="${PUSH:-1}"

IMAGE="${REGISTRY}/${IMAGE_NS}/instrumentation-nginx:${TAG}"
FAT_TAG="${FAT_TAG:-crnet-1.1.0}"
FAT_IMAGE="${REGISTRY}/${IMAGE_NS}/instrumentation-nginx:${FAT_TAG}"

echo "=== Building ${IMAGE} (${PLATFORM}) ==="

TARGET="final"
if [ "${SKIP_NGINX_MODULE_BUILD:-0}" = "1" ]; then
  TARGET="final-smoke"
  FAT_IMAGE=""
fi

BUILD_ARGS=(
  --platform "$PLATFORM"
  --provenance=false
  --sbom=false
  --target "$TARGET"
  -f "${AGENT_DIR}/Dockerfile.agent-nginx"
  -t "$IMAGE"
)
if [ -n "$FAT_IMAGE" ]; then
  BUILD_ARGS+=(-t "$FAT_IMAGE")
fi

if [ -n "${OTEL_WEBSERVER_GIT_REF:-}" ]; then
  BUILD_ARGS+=(--build-arg "OTEL_WEBSERVER_GIT_REF=${OTEL_WEBSERVER_GIT_REF}")
fi

if [ "$PUSH" = "1" ]; then
  docker buildx build "${BUILD_ARGS[@]}" --push "$AGENT_DIR"
else
  docker buildx build "${BUILD_ARGS[@]}" --load "$AGENT_DIR"
fi

echo "=== Done: ${IMAGE} ==="
