<?php
/**
 * CRNET APM HTTP payload capture for PHP auto_prepend_file.
 *
 * php://input is reusable on PHP 7+ so reading it here does not steal the
 * body from the application. Multipart uploads are skipped (php://input is
 * empty for those); $_POST is used instead.
 */
if (getenv('CRNET_HTTP_CAPTURE') === 'false' || getenv('CRNET_HTTP_CAPTURE') === '0') {
    return;
}

const CRNET_HTTP_CAPTURE_MAX = intval(getenv('CRNET_HTTP_CAPTURE_MAX_BYTES') ?: '4096');

function crnet_http_capture_redact(string $raw): string
{
    $text = trim($raw);
    if ($text === '') {
        return '';
    }
    $secret = '/password|passwd|pwd|secret|token|authorization|cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|private[_-]?key|ssn/i';
    $json = json_decode($text, true);
    if (is_array($json)) {
        array_walk_recursive($json, function (&$value, $key) use ($secret) {
            if (is_string($key) && preg_match($secret, $key)) {
                $value = '[redacted]';
            }
        });
        $encoded = json_encode($json, JSON_UNESCAPED_UNICODE);
        if (is_string($encoded)) {
            $text = $encoded;
        }
    } elseif (strpos($text, '=') !== false) {
        $parts = explode('&', $text);
        foreach ($parts as $i => $part) {
            $eq = strpos($part, '=');
            if ($eq === false) {
                continue;
            }
            $key = substr($part, 0, $eq);
            if (preg_match($secret, $key)) {
                $parts[$i] = $key . '=[redacted]';
            }
        }
        $text = implode('&', $parts);
    }
    if (strlen($text) > CRNET_HTTP_CAPTURE_MAX) {
        $text = substr($text, 0, CRNET_HTTP_CAPTURE_MAX) . '…[truncated]';
    }
    return $text;
}

function crnet_http_capture_set(string $attr, string $raw): void
{
    $text = crnet_http_capture_redact($raw);
    if ($text === '') {
        return;
    }
    if (!class_exists('OpenTelemetry\\API\\Trace\\Span')) {
        return;
    }
    try {
        $span = \OpenTelemetry\API\Trace\Span::getCurrent();
        if ($span && $span->isRecording()) {
            $span->setAttribute($attr, $text);
        }
    } catch (Throwable $e) {
        return;
    }
}

$contentType = strtolower((string) ($_SERVER['CONTENT_TYPE'] ?? ''));
$raw = '';
if (strpos($contentType, 'multipart/form-data') === false) {
    $raw = (string) file_get_contents('php://input');
}
if ($raw === '' && !empty($_POST)) {
    $encoded = json_encode($_POST);
    $raw = is_string($encoded) ? $encoded : '';
}
if ($raw !== '') {
    crnet_http_capture_set('http.request.body', $raw);
}

ob_start(function (string $buffer) {
    if ($buffer !== '') {
        crnet_http_capture_set('http.response.body', $buffer);
    }
    return $buffer;
});
