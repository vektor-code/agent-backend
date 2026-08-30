package net.cloudraft.apm.httpcapture;

import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

final class Redact {
    private static final Pattern SECRET_KEY = Pattern.compile(
        "password|passwd|pwd|secret|token|authorization|cookie|api[_-]?key|"
            + "access[_-]?token|refresh[_-]?token|private[_-]?key|ssn",
        Pattern.CASE_INSENSITIVE
    );
    private static final Pattern JSON_SECRET = Pattern.compile(
        "(\"(?:password|passwd|pwd|secret|token|authorization|cookie|api[_-]?key|"
            + "access[_-]?token|refresh[_-]?token|private[_-]?key|ssn)\"\\s*:\\s*)(\"[^\"]*\"|[^,}\\]]+)",
        Pattern.CASE_INSENSITIVE
    );

    private Redact() {}

    static String body(String raw, int max) {
        if (raw == null) {
            return "";
        }
        String text = raw.trim();
        if (text.isEmpty()) {
            return "";
        }
        if (text.startsWith("{") || text.startsWith("[")) {
            Matcher m = JSON_SECRET.matcher(text);
            text = m.replaceAll("$1\"[redacted]\"");
        } else if (text.contains("=")) {
            StringBuilder sb = new StringBuilder();
            for (String part : text.split("&")) {
                int eq = part.indexOf('=');
                if (eq > 0 && SECRET_KEY.matcher(part.substring(0, eq)).find()) {
                    part = part.substring(0, eq) + "=[redacted]";
                }
                if (sb.length() > 0) {
                    sb.append('&');
                }
                sb.append(part);
            }
            text = sb.toString();
        }
        if (text.length() > max) {
            return text.substring(0, max) + "…[truncated]";
        }
        return text;
    }
}
