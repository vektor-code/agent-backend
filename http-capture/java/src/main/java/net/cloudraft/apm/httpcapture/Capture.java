package net.cloudraft.apm.httpcapture;

import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.Locale;

final class Capture {
    private static final int MAX = parseMax();
    private static final boolean ENABLED = parseEnabled();

    private Capture() {}

    static boolean enabled() {
        return ENABLED;
    }

    static int maxBytes() {
        return MAX;
    }

    static void recordRequest(Object request) {
        if (!ENABLED || request == null) {
            return;
        }
        try {
            String contentType = header(request, "Content-Type");
            if (!textish(contentType)) {
                setAttribute("http.request.body.omitted", "non-text content-type");
                return;
            }
            Integer length = contentLength(request);
            if (length != null && length == 0) {
                return;
            }
            // Prefer already-parsed parameters so we never steal the input stream.
            String form = formBody(request);
            if (form != null && !form.isEmpty()) {
                setAttribute("http.request.body", Redact.body(form, MAX));
            }
        } catch (Throwable ignored) {
            // never break the application
        }
    }

    static Object wrapSpringRequest(Object request) {
        if (!ENABLED || request == null) {
            return null;
        }
        try {
            ClassLoader cl = request.getClass().getClassLoader();
            Class<?> wrapper = Class.forName("org.springframework.web.util.ContentCachingRequestWrapper", true, cl);
            Class<?> servletReq = Class.forName(servletType(request, "http.HttpServletRequest"), true, cl);
            return wrapper.getConstructor(servletReq, int.class).newInstance(request, MAX);
        } catch (Throwable ignored) {
            return null;
        }
    }

    static Object wrapSpringResponse(Object response) {
        if (!ENABLED || response == null) {
            return null;
        }
        try {
            ClassLoader cl = response.getClass().getClassLoader();
            Class<?> wrapper = Class.forName("org.springframework.web.util.ContentCachingResponseWrapper", true, cl);
            Class<?> servletRes = Class.forName(servletType(response, "http.HttpServletResponse"), true, cl);
            return wrapper.getConstructor(servletRes).newInstance(response);
        } catch (Throwable ignored) {
            return null;
        }
    }

    static void flushSpring(Object request, Object response) {
        try {
            if (request != null) {
                byte[] body = invokeBytes(request, "getContentAsByteArray");
                String ct = header(request, "Content-Type");
                if (body != null && body.length > 0 && textish(ct)) {
                    setAttribute("http.request.body", Redact.body(new String(body, StandardCharsets.UTF_8), MAX));
                }
            }
            if (response != null) {
                byte[] body = invokeBytes(response, "getContentAsByteArray");
                String ct = header(response, "Content-Type");
                if (body != null && body.length > 0 && textish(ct)) {
                    setAttribute("http.response.body", Redact.body(new String(body, StandardCharsets.UTF_8), MAX));
                }
                try {
                    response.getClass().getMethod("copyBodyToResponse").invoke(response);
                } catch (Throwable ignored) {
                    // wrapper may already have been copied
                }
            }
        } catch (Throwable ignored) {
            // never break the application
        }
    }

    static Object wrapGenericRequest(Object request) {
        if (!ENABLED || request == null || alreadyWrapped(request)) {
            return null;
        }
        Object spring = wrapSpringRequest(request);
        if (spring != null) {
            return spring;
        }
        return ServletWrap.wrapRequest(request);
    }

    static Object wrapGenericResponse(Object response) {
        if (!ENABLED || response == null || alreadyWrapped(response)) {
            return null;
        }
        Object spring = wrapSpringResponse(response);
        if (spring != null) {
            return spring;
        }
        return ServletWrap.wrapResponse(response);
    }

    static void flushGeneric(Object request, Object response) {
        if (isSpringWrapper(request) || isSpringWrapper(response)) {
            flushSpring(request, response);
            return;
        }
        ServletWrap.flush(request, response);
    }

    static String headerPublic(Object target, String name) {
        return header(target, name);
    }

    static void recordRawRequest(byte[] body, String contentType) {
        recordRaw("http.request.body", body, contentType);
    }

    static void recordRawResponse(byte[] body, String contentType) {
        recordRaw("http.response.body", body, contentType);
    }

    private static void recordRaw(String attr, byte[] body, String contentType) {
        if (!ENABLED || body == null || body.length == 0) {
            return;
        }
        if (!textish(contentType)) {
            setAttribute(attr + ".omitted", "non-text content-type");
            return;
        }
        int take = Math.min(body.length, MAX);
        setAttribute(attr, Redact.body(new String(body, 0, take, StandardCharsets.UTF_8), MAX));
    }

    private static boolean alreadyWrapped(Object obj) {
        String name = obj.getClass().getName();
        return name.contains("cloudraft.apm.httpcapture")
            || name.contains("ContentCachingRequestWrapper")
            || name.contains("ContentCachingResponseWrapper");
    }

    private static boolean isSpringWrapper(Object obj) {
        return obj != null && obj.getClass().getName().contains("ContentCaching");
    }

    private static byte[] invokeBytes(Object target, String method) {
        try {
            Object value = target.getClass().getMethod(method).invoke(target);
            return value instanceof byte[] ? (byte[]) value : null;
        } catch (Throwable ignored) {
            return null;
        }
    }

    private static String formBody(Object request) {
        try {
            Object map = request.getClass().getMethod("getParameterMap").invoke(request);
            if (!(map instanceof java.util.Map)) {
                return null;
            }
            StringBuilder sb = new StringBuilder();
            for (Object entryObj : ((java.util.Map<?, ?>) map).entrySet()) {
                java.util.Map.Entry<?, ?> entry = (java.util.Map.Entry<?, ?>) entryObj;
                Object raw = entry.getValue();
                String value;
                if (raw instanceof String[]) {
                    value = String.join(",", (String[]) raw);
                } else {
                    value = String.valueOf(raw);
                }
                if (sb.length() > 0) {
                    sb.append('&');
                }
                sb.append(entry.getKey()).append('=').append(value);
            }
            return sb.toString();
        } catch (Throwable ignored) {
            return null;
        }
    }

    private static String header(Object target, String name) {
        try {
            Method m = target.getClass().getMethod("getHeader", String.class);
            Object value = m.invoke(target, name);
            return value == null ? "" : String.valueOf(value);
        } catch (Throwable ignored) {
            return "";
        }
    }

    private static Integer contentLength(Object request) {
        try {
            Object value = request.getClass().getMethod("getContentLength").invoke(request);
            return value instanceof Integer ? (Integer) value : null;
        } catch (Throwable ignored) {
            return null;
        }
    }

    private static boolean textish(String contentType) {
        if (contentType == null || contentType.isEmpty()) {
            return true;
        }
        String ct = contentType.toLowerCase(Locale.ROOT);
        return ct.contains("json") || ct.contains("xml") || ct.contains("text/")
            || ct.contains("javascript") || ct.contains("form-urlencoded") || ct.contains("graphql");
    }

    private static String servletType(Object obj, String suffix) {
        String name = obj.getClass().getName();
        if (name.startsWith("jakarta.")) {
            return "jakarta.servlet." + suffix;
        }
        return "javax.servlet." + suffix;
    }

    static void setAttribute(String key, String value) {
        if (value == null || value.isEmpty()) {
            return;
        }
        try {
            Class<?> spanClass = Class.forName("io.opentelemetry.api.trace.Span");
            Object span = spanClass.getMethod("current").invoke(null);
            if (span == null) {
                return;
            }
            Boolean recording = (Boolean) spanClass.getMethod("isRecording").invoke(span);
            if (recording == null || !recording) {
                return;
            }
            spanClass.getMethod("setAttribute", String.class, String.class).invoke(span, key, value);
        } catch (Throwable ignored) {
            // OTel API not visible on this classloader
        }
    }

    private static int parseMax() {
        try {
            return Integer.parseInt(System.getenv().getOrDefault("CRNET_HTTP_CAPTURE_MAX_BYTES", "4096"));
        } catch (Exception e) {
            return 4096;
        }
    }

    private static boolean parseEnabled() {
        String raw = System.getenv().getOrDefault("CRNET_HTTP_CAPTURE", "true");
        return !raw.equalsIgnoreCase("0") && !raw.equalsIgnoreCase("false") && !raw.equalsIgnoreCase("off");
    }
}
