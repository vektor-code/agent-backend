'use strict';

/**
 * CRNET APM HTTP payload capture for injected Node auto-instrumentation.
 * Copies truncated, redacted request/response bodies onto the current span.
 * Load AFTER the official autoinstrumentation.js via NODE_OPTIONS --require.
 */
const MAX = parseInt(process.env.CRNET_HTTP_CAPTURE_MAX_BYTES || '4096', 10);
const ENABLED = !/^(0|false|off)$/i.test(process.env.CRNET_HTTP_CAPTURE || 'true');
const SECRET = /(password|passwd|pwd|secret|token|authorization|cookie|set-cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|private[_-]?key|credit|card|ssn|session)/i;
const TEXTISH = /json|xml|text\/|javascript|x-www-form-urlencoded|graphql/i;

function api() {
  try {
    return require('@opentelemetry/api');
  } catch {
    return null;
  }
}

function currentSpan() {
  const otel = api();
  if (!otel) return null;
  try {
    const span = otel.trace.getActiveSpan();
    if (span && span.isRecording()) return span;
  } catch {
    return null;
  }
  return null;
}

function truncate(text) {
  if (!text) return '';
  return text.length <= MAX ? text : `${text.slice(0, MAX)}…[truncated]`;
}

function redactJson(value, key) {
  if (key && SECRET.test(String(key))) return '[redacted]';
  if (Array.isArray(value)) return value.map((item) => redactJson(item, key));
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) out[k] = redactJson(v, k);
    return out;
  }
  return value;
}

function redact(text) {
  const raw = String(text || '').trim();
  if (!raw) return '';
  if (raw[0] === '{' || raw[0] === '[') {
    try {
      return truncate(JSON.stringify(redactJson(JSON.parse(raw))));
    } catch {
      /* fall through */
    }
  }
  if (raw.includes('=')) {
    return truncate(
      raw
        .split('&')
        .map((part) => {
          const i = part.indexOf('=');
          if (i === -1) return part;
          const key = part.slice(0, i);
          return SECRET.test(key) ? `${key}=[redacted]` : part;
        })
        .join('&')
    );
  }
  return truncate(raw);
}

function asText(chunk) {
  if (chunk == null) return '';
  if (Buffer.isBuffer(chunk)) return chunk.toString('utf8');
  if (typeof chunk === 'string') return chunk;
  try {
    return Buffer.from(chunk).toString('utf8');
  } catch {
    return String(chunk);
  }
}

function setBody(attr, raw, contentType) {
  if (!ENABLED) return;
  const span = currentSpan();
  if (!span) return;
  const ct = String(contentType || '');
  if (ct && !TEXTISH.test(ct)) {
    span.setAttribute(`${attr}.omitted`, 'non-text content-type');
    return;
  }
  const text = redact(asText(raw));
  if (text) span.setAttribute(attr, text);
}

function toBuffer(chunk, encoding) {
  if (chunk == null) return null;
  if (Buffer.isBuffer(chunk)) return chunk;
  if (typeof chunk === 'string') return Buffer.from(chunk, typeof encoding === 'string' ? encoding : 'utf8');
  try {
    return Buffer.from(chunk);
  } catch {
    return Buffer.from(String(chunk));
  }
}

function tapIncoming(req, res) {
  const chunks = [];
  let size = 0;
  req.on('data', (chunk) => {
    if (size >= MAX) return;
    const buf = toBuffer(chunk) || Buffer.alloc(0);
    const next = buf.subarray(0, Math.max(0, MAX - size));
    chunks.push(next);
    size += next.length;
  });
  req.on('end', () => {
    setBody('http.request.body', Buffer.concat(chunks), req.headers && req.headers['content-type']);
  });

  const out = [];
  let outSize = 0;
  const origWrite = res.write;
  const origEnd = res.end;
  const take = (chunk, encoding) => {
    if (chunk == null || outSize >= MAX) return;
    const buf = toBuffer(chunk, encoding);
    if (!buf) return;
    const next = buf.subarray(0, Math.max(0, MAX - outSize));
    out.push(next);
    outSize += next.length;
  };
  res.write = function crnetWrite(chunk, encoding, cb) {
    take(chunk, encoding);
    return origWrite.call(this, chunk, encoding, cb);
  };
  res.end = function crnetEnd(chunk, encoding, cb) {
    take(chunk, encoding);
    setBody('http.response.body', Buffer.concat(out), res.getHeader && res.getHeader('content-type'));
    return origEnd.call(this, chunk, encoding, cb);
  };
}

function patchServer(mod) {
  if (!mod || !mod.Server || !mod.Server.prototype) return;
  const orig = mod.Server.prototype.emit;
  if (orig.__crnetHttpCapture) return;
  function emit(event, req, res) {
    if (event === 'request' && req && res) {
      try {
        tapIncoming(req, res);
      } catch {
        /* never break the app */
      }
    }
    return orig.apply(this, arguments);
  }
  emit.__crnetHttpCapture = true;
  mod.Server.prototype.emit = emit;
}

function patchClient(mod) {
  if (!mod || typeof mod.request !== 'function') return;
  const orig = mod.request;
  if (orig.__crnetHttpCapture) return;
  function request(options, callback) {
    const req = orig.call(this, options, callback);
    try {
      const origWrite = req.write;
      const origEnd = req.end;
      const chunks = [];
      let size = 0;
      const take = (chunk, encoding) => {
        if (chunk == null || size >= MAX) return;
        const buf = toBuffer(chunk, encoding);
        if (!buf) return;
        const next = buf.subarray(0, Math.max(0, MAX - size));
        chunks.push(next);
        size += next.length;
      };
      req.write = function crnetClientWrite(chunk, encoding, cb) {
        take(chunk, encoding);
        return origWrite.call(this, chunk, encoding, cb);
      };
      req.end = function crnetClientEnd(chunk, encoding, cb) {
        take(chunk, encoding);
        const headers = (options && options.headers) || {};
        const ct = headers['Content-Type'] || headers['content-type'] || '';
        setBody('http.request.body', Buffer.concat(chunks), ct);
        return origEnd.call(this, chunk, encoding, cb);
      };
      req.on('response', (res) => {
        const incoming = [];
        let incomingSize = 0;
        res.on('data', (chunk) => {
          if (incomingSize >= MAX) return;
          const buf = toBuffer(chunk) || Buffer.alloc(0);
          const next = buf.subarray(0, Math.max(0, MAX - incomingSize));
          incoming.push(next);
          incomingSize += next.length;
        });
        res.on('end', () => {
          setBody(
            'http.response.body',
            Buffer.concat(incoming),
            res.headers && (res.headers['content-type'] || res.headers['Content-Type'])
          );
        });
      });
    } catch {
      /* never break the app */
    }
    return req;
  }
  request.__crnetHttpCapture = true;
  mod.request = request;
}

try {
  patchServer(require('http'));
  patchServer(require('https'));
  patchClient(require('http'));
  patchClient(require('https'));
} catch {
  /* ignore */
}

module.exports = { redact, setBody, MAX, ENABLED };
