"""CRNET APM HTTP payload capture for injected Python auto-instrumentation.

Copies a truncated, redacted request/response body onto the current span as
http.request.body / http.response.body. Official OTel SDKs never send bodies;
this hook is loaded from sitecustomize after the distro starts.

Frameworks are patched on first import so sitecustomize can run before Flask,
Django, or Starlette are loaded.
"""
from __future__ import annotations

import builtins
import json
import os
import re
import sys
from typing import Any, Callable, Dict, Set

MAX_BYTES = int(os.environ.get("CRNET_HTTP_CAPTURE_MAX_BYTES", "4096"))
ENABLED = os.environ.get("CRNET_HTTP_CAPTURE", "true").lower() not in ("0", "false", "off")
_SECRET = re.compile(
    r"(password|passwd|pwd|secret|token|authorization|cookie|set-cookie|api[_-]?key|"
    r"access[_-]?token|refresh[_-]?token|private[_-]?key|credit|card|ssn|session)",
    re.I,
)
_TEXTISH = (
    "json",
    "xml",
    "text/",
    "javascript",
    "x-www-form-urlencoded",
    "graphql",
)

_patched: Set[str] = set()
_orig_import = builtins.__import__


def _textish(content_type: str) -> bool:
    ct = (content_type or "").lower()
    if not ct:
        return True
    return any(part in ct for part in _TEXTISH)


def _truncate(value: str, limit: int = MAX_BYTES) -> str:
    if len(value) <= limit:
        return value
    return value[:limit] + "…[truncated]"


def _redact_value(key: str, value: Any) -> Any:
    if _SECRET.search(str(key or "")):
        return "[redacted]"
    if isinstance(value, dict):
        return {k: _redact_value(k, v) for k, v in value.items()}
    if isinstance(value, list):
        return [_redact_value(key, item) for item in value]
    return value


def redact_body(raw: str) -> str:
    text = (raw or "").strip()
    if not text:
        return ""
    if text[0] in "{[":
        try:
            parsed = json.loads(text)
            return _truncate(json.dumps(_redact_value("", parsed), ensure_ascii=False))
        except Exception:
            pass
    if "=" in text:
        out = []
        for part in text.split("&"):
            if "=" in part:
                key, val = part.split("=", 1)
                if _SECRET.search(key):
                    out.append(key + "=[redacted]")
                    continue
            out.append(part)
        return _truncate("&".join(out))
    return _truncate(text)


def _span():
    try:
        from opentelemetry import trace  # type: ignore
    except Exception:
        return None
    span = trace.get_current_span()
    if span is None:
        return None
    try:
        if not span.is_recording():
            return None
    except Exception:
        return None
    return span


def set_body(attr: str, raw: Any, content_type: str = "") -> None:
    if not ENABLED or raw is None:
        return
    if not _textish(content_type):
        span = _span()
        if span is not None:
            span.set_attribute(attr + ".omitted", "non-text content-type")
        return
    if isinstance(raw, bytes):
        text = raw.decode("utf-8", "replace")
    elif isinstance(raw, memoryview):
        text = raw.tobytes().decode("utf-8", "replace")
    else:
        text = str(raw)
    text = redact_body(text)
    if not text:
        return
    span = _span()
    if span is None:
        return
    span.set_attribute(attr, text)


class _BytesReplay:
    def __init__(self, data: bytes) -> None:
        self._buf = data
        self._pos = 0

    def read(self, size: int = -1) -> bytes:
        if size is None or size < 0:
            out = self._buf[self._pos :]
            self._pos = len(self._buf)
            return out
        out = self._buf[self._pos : self._pos + size]
        self._pos += len(out)
        return out

    def readline(self, size: int = -1) -> bytes:
        if self._pos >= len(self._buf):
            return b""
        nl = self._buf.find(b"\n", self._pos)
        end = len(self._buf) if nl < 0 else nl + 1
        if size is not None and size >= 0:
            end = min(end, self._pos + size)
        out = self._buf[self._pos : end]
        self._pos += len(out)
        return out

    def readlines(self, hint: int = -1) -> list:
        lines = []
        while True:
            line = self.readline()
            if not line:
                break
            lines.append(line)
        return lines

    def close(self) -> None:
        return None

    def __iter__(self):
        return self

    def __next__(self) -> bytes:
        line = self.readline()
        if not line:
            raise StopIteration
        return line


def _patch_wsgi() -> None:
    try:
        from wsgiref import simple_server
    except Exception:
        return

    original = simple_server.WSGIRequestHandler.get_environ

    def get_environ(self):  # type: ignore[no-untyped-def]
        environ = original(self)
        try:
            length = int(environ.get("CONTENT_LENGTH") or 0)
        except Exception:
            length = 0
        if length <= 0 or length > MAX_BYTES * 4:
            return environ
        body = environ["wsgi.input"].read(min(length, MAX_BYTES))
        set_body("http.request.body", body, environ.get("CONTENT_TYPE", ""))
        environ["wsgi.input"] = _BytesReplay(body)
        return environ

    simple_server.WSGIRequestHandler.get_environ = get_environ  # type: ignore[method-assign]


def _patch_flask() -> None:
    try:
        import flask  # type: ignore
    except Exception:
        return

    original = flask.Flask.full_dispatch_request

    def full_dispatch_request(self):  # type: ignore[no-untyped-def]
        req = flask.request
        try:
            set_body("http.request.body", req.get_data(cache=True), req.content_type or "")
        except Exception:
            pass
        response = original(self)
        try:
            data = response.get_data()
            set_body("http.response.body", data, response.content_type or "")
        except Exception:
            pass
        return response

    flask.Flask.full_dispatch_request = full_dispatch_request  # type: ignore[method-assign]


def _patch_django() -> None:
    try:
        from django.http.request import HttpRequest  # type: ignore
        from django.http.response import HttpResponse  # type: ignore
    except Exception:
        return

    if getattr(HttpRequest, "_crnet_http_capture", False):
        return

    original_body = HttpRequest.body.fget  # type: ignore[attr-defined]

    def body(self):  # type: ignore[no-untyped-def]
        data = original_body(self)
        try:
            ct = ""
            meta = getattr(self, "META", None) or {}
            ct = meta.get("CONTENT_TYPE") or getattr(self, "content_type", "") or ""
            set_body("http.request.body", data, ct)
        except Exception:
            pass
        return data

    HttpRequest.body = property(body)  # type: ignore[method-assign]
    HttpRequest._crnet_http_capture = True  # type: ignore[attr-defined]

    original_content = HttpResponse.content.fget  # type: ignore[attr-defined]
    original_content_set = HttpResponse.content.fset  # type: ignore[attr-defined]

    def content_get(self):  # type: ignore[no-untyped-def]
        data = original_content(self)
        try:
            set_body("http.response.body", data, getattr(self, "content_type", "") or "")
        except Exception:
            pass
        return data

    def content_set(self, value):  # type: ignore[no-untyped-def]
        original_content_set(self, value)
        try:
            set_body("http.response.body", value, getattr(self, "content_type", "") or "")
        except Exception:
            pass

    HttpResponse.content = property(content_get, content_set)  # type: ignore[method-assign]


def _patch_starlette() -> None:
    try:
        from starlette.middleware.base import BaseHTTPMiddleware  # type: ignore
        from starlette.requests import Request  # type: ignore
    except Exception:
        return

    if getattr(Request, "_crnet_http_capture", False):
        return

    original_body = Request.body

    async def body(self):  # type: ignore[no-untyped-def]
        data = await original_body(self)
        set_body("http.request.body", data, self.headers.get("content-type", ""))
        return data

    Request.body = body  # type: ignore[method-assign]
    Request._crnet_http_capture = True  # type: ignore[attr-defined]

    try:
        from starlette.applications import Starlette  # type: ignore
    except Exception:
        return

    class CaptureMiddleware(BaseHTTPMiddleware):
        async def dispatch(self, request, call_next):  # type: ignore[no-untyped-def]
            try:
                await request.body()
            except Exception:
                pass
            response = await call_next(request)
            try:
                chunks = []
                async for chunk in response.body_iterator:
                    chunks.append(chunk if isinstance(chunk, bytes) else bytes(chunk))
                payload = b"".join(chunks)
                set_body(
                    "http.response.body",
                    payload,
                    response.headers.get("content-type", ""),
                )

                async def _replay():
                    yield payload

                response.body_iterator = _replay()
            except Exception:
                pass
            return response

    original_init = Starlette.__init__
    if getattr(Starlette, "_crnet_http_capture", False):
        return

    def __init__(self, *args, **kwargs):  # type: ignore[no-untyped-def]
        original_init(self, *args, **kwargs)
        try:
            self.add_middleware(CaptureMiddleware)
        except Exception:
            pass

    Starlette.__init__ = __init__  # type: ignore[method-assign]
    Starlette._crnet_http_capture = True  # type: ignore[attr-defined]


def _patch_urllib3() -> None:
    try:
        import urllib3.connection  # type: ignore
    except Exception:
        return
    original = urllib3.connection.HTTPConnection.request
    if getattr(original, "_crnet_http_capture", False):
        return

    def request(self, method, url, body=None, headers=None, **kwargs):  # type: ignore[no-untyped-def]
        ct = ""
        if headers:
            ct = headers.get("Content-Type") or headers.get("content-type") or ""
        set_body("http.request.body", body, ct)
        return original(self, method, url, body=body, headers=headers, **kwargs)

    request._crnet_http_capture = True  # type: ignore[attr-defined]
    urllib3.connection.HTTPConnection.request = request  # type: ignore[method-assign]


def _patch_http_client() -> None:
    try:
        import http.client as http_client
    except Exception:
        return
    original = http_client.HTTPConnection.request
    if getattr(original, "_crnet_http_capture", False):
        return

    def request(self, method, url, body=None, headers=None, encode_chunked=False):  # type: ignore[no-untyped-def]
        headers = headers or {}
        set_body(
            "http.request.body",
            body,
            headers.get("Content-Type") or headers.get("content-type") or "",
        )
        return original(self, method, url, body=body, headers=headers, encode_chunked=encode_chunked)

    request._crnet_http_capture = True  # type: ignore[attr-defined]
    http_client.HTTPConnection.request = request  # type: ignore[method-assign]


def _patch_requests() -> None:
    try:
        import requests  # type: ignore
    except Exception:
        return
    original = requests.sessions.Session.send
    if getattr(original, "_crnet_http_capture", False):
        return

    def send(self, request, **kwargs):  # type: ignore[no-untyped-def]
        try:
            ct = ""
            if request.headers:
                ct = request.headers.get("Content-Type") or request.headers.get("content-type") or ""
            set_body("http.request.body", request.body, ct)
        except Exception:
            pass
        response = original(self, request, **kwargs)
        try:
            ct = response.headers.get("Content-Type") or response.headers.get("content-type") or ""
            set_body("http.response.body", response.content, ct)
        except Exception:
            pass
        return response

    send._crnet_http_capture = True  # type: ignore[attr-defined]
    requests.sessions.Session.send = send  # type: ignore[method-assign]


def _patch_httpx() -> None:
    try:
        import httpx  # type: ignore
    except Exception:
        return
    original = httpx.Client.request
    if getattr(original, "_crnet_http_capture", False):
        return

    def request(self, method, url, **kwargs):  # type: ignore[no-untyped-def]
        try:
            content = kwargs.get("content") or kwargs.get("data") or kwargs.get("json")
            if kwargs.get("json") is not None and not isinstance(content, (bytes, str)):
                content = json.dumps(kwargs["json"])
            headers = kwargs.get("headers") or {}
            ct = ""
            if hasattr(headers, "get"):
                ct = headers.get("content-type") or headers.get("Content-Type") or ""
            set_body("http.request.body", content, ct)
        except Exception:
            pass
        return original(self, method, url, **kwargs)

    request._crnet_http_capture = True  # type: ignore[attr-defined]
    httpx.Client.request = request  # type: ignore[method-assign]


_PATCHERS: Dict[str, Callable[[], None]] = {
    "wsgiref.simple_server": _patch_wsgi,
    "flask": _patch_flask,
    "django.http.request": _patch_django,
    "starlette.requests": _patch_starlette,
    "urllib3.connection": _patch_urllib3,
    "http.client": _patch_http_client,
    "requests": _patch_requests,
    "httpx": _patch_httpx,
}


def _try_patch_loaded() -> None:
    for key, fn in _PATCHERS.items():
        if key in _patched:
            continue
        if key not in sys.modules:
            continue
        try:
            fn()
            _patched.add(key)
        except Exception:
            pass


def _import_hook(name, globals=None, locals=None, fromlist=(), level=0):  # type: ignore[no-untyped-def]
    mod = _orig_import(name, globals, locals, fromlist, level)
    _try_patch_loaded()
    return mod


def install() -> None:
    if not ENABLED:
        return
    _try_patch_loaded()
    if builtins.__import__ is not _import_hook:
        builtins.__import__ = _import_hook  # type: ignore[assignment]


install()
sys.modules.setdefault("crnet_http_capture", sys.modules[__name__])
