# Loaded from the auto-instrumentation volume (PYTHONPATH). Keep the official
# distro sitecustomize working: we only append this import when wrapping the
# image. When used as a first-on-path sitecustomize, also try the operator file.
try:
    import crnet_http_capture  # noqa: F401
except Exception:
    pass
