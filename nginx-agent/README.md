# CRNET instrumentation-nginx (fat webserver agent)

Datadog-style **one agent image, many exact nginx module builds**. The OpenTelemetry operator picks
`/opt/opentelemetry/WebServerModule/Nginx/<exact-semver>/ngx_http_opentelemetry_module.so` at inject
time (from `nginx -v` via `version.txt`). Semver must match exactly — `1.31.4` ≠ `1.31.0`.

Upstream `autoinstrumentation-apache-httpd:1.0.4` only ships **1.24.0** and **1.25.3**. This image
extends that layout with the versions in `version.properties` (includes highping **1.31.4**).

## Layout (operator-compatible)

Same as official `ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-apache-httpd`:

```
/opt/opentelemetry/
  WebServerModule/Nginx/{1.24.0,1.25.3,...}/ngx_http_opentelemetry_module.so
  WebServerModule/Apache/libmod_apache_otel.so
  WebServerModule/Apache/libmod_apache_otel22.so
  sdk_lib/lib/...
  MODULES.txt          # CRNET: one nginx semver per line
```

Apache httpd modules come from the upstream `:1.0.4` base (glibc). Nginx modules are glibc-built on
AlmaLinux 8 — **Alpine/musl nginx images cannot load these `.so` files**; use Debian/RHEL-based nginx
or leave inject off.

## Build

From repo root (or `code/agent-backend/`):

```bash
export REGISTRY=git.cloudraft.net:5050
export IMAGE_NS=devops/images/crnet-apm
export TAG=prod
./code/agent-backend/nginx-agent/build.sh
```

Or:

```bash
docker buildx build -f code/agent-backend/Dockerfile.agent-nginx \
  --platform linux/amd64 \
  -t "$REGISTRY/$IMAGE_NS/instrumentation-nginx:$TAG" \
  code/agent-backend
```

The builder stage clones `open-telemetry/opentelemetry-cpp-contrib` at `OTEL_WEBSERVER_GIT_REF`
(default `webserver/v1.0.4`), overlays this `version.properties`, and runs the upstream AlmaLinux 8
gradle build (`assembleWebServerModule`). Expect **1–3+ hours** on first build; cache the builder
layer in CI.

Set `SKIP_NGINX_MODULE_BUILD=1` to produce a smoke image (upstream modules only) without compiling.

## Push / deploy

Image is referenced as **`instrumentation-nginx`** (not `autoinstrumentation-apache-httpd`). Charts set:

- `OTEL_NGINX_IMAGE` / `OTEL_APACHE_IMAGE` → `.../instrumentation-nginx:{tag}`
- Agent-backend `nginx_compat` tag **`crnet-1.1.0`** maps to all versions in `version.properties`.

## Adjusting versions

1. Edit `version.properties` and matching `crnet-1.1.0` entry in `nginx_compat.go` (both backends).
2. Rebuild and push the image.
3. Roll agent-backend so compatibility gates pick up the new matrix.
