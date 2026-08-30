package net.cloudraft.apm.httpcapture;

import net.bytebuddy.ByteBuddy;
import net.bytebuddy.dynamic.loading.ClassLoadingStrategy;
import net.bytebuddy.implementation.MethodDelegation;
import net.bytebuddy.implementation.bind.annotation.AllArguments;
import net.bytebuddy.implementation.bind.annotation.RuntimeType;
import net.bytebuddy.implementation.bind.annotation.This;
import net.bytebuddy.matcher.ElementMatchers;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.FilterOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Caches servlet request/response bodies without depending on Spring.
 * Wrapper classes are generated per application classloader (javax vs jakarta).
 */
final class ServletWrap {
    private static final Map<ClassLoader, Classes> CACHE = new ConcurrentHashMap<>();

    private ServletWrap() {}

    static Object wrapRequest(Object request) {
        if (request == null) {
            return null;
        }
        try {
            Classes classes = classes(request.getClass().getClassLoader(), request);
            if (classes == null) {
                return null;
            }
            byte[] body = readBody(request);
            Object wrapper = classes.requestCtor.newInstance(request);
            classes.requestBody.set(wrapper, body == null ? new byte[0] : body);
            if (classes.requestReplay != null) {
                classes.requestReplay.set(wrapper, classes.replayIn);
            }
            return wrapper;
        } catch (Throwable ignored) {
            return null;
        }
    }

    static Object wrapResponse(Object response) {
        if (response == null) {
            return null;
        }
        try {
            Classes classes = classes(response.getClass().getClassLoader(), response);
            if (classes == null) {
                return null;
            }
            Object wrapper = classes.responseCtor.newInstance(response);
            classes.responseBuffer.set(wrapper, new ByteArrayOutputStream());
            if (classes.responseTee != null) {
                classes.responseTee.set(wrapper, classes.teeOut);
            }
            return wrapper;
        } catch (Throwable ignored) {
            return null;
        }
    }

    static void flush(Object request, Object response) {
        try {
            if (request != null) {
                Field bodyField = fieldNamed(request.getClass(), "crnetBody");
                if (bodyField != null) {
                    bodyField.setAccessible(true);
                    Object raw = bodyField.get(request);
                    if (raw instanceof byte[] && ((byte[]) raw).length > 0) {
                        String ct = Capture.headerPublic(request, "Content-Type");
                        Capture.recordRawRequest((byte[]) raw, ct);
                    }
                }
            }
            if (response != null) {
                Field bufField = fieldNamed(response.getClass(), "crnetBuffer");
                if (bufField != null) {
                    bufField.setAccessible(true);
                    Object raw = bufField.get(response);
                    if (raw instanceof ByteArrayOutputStream) {
                        byte[] body = ((ByteArrayOutputStream) raw).toByteArray();
                        if (body.length > 0) {
                            String ct = Capture.headerPublic(response, "Content-Type");
                            Capture.recordRawResponse(body, ct);
                        }
                    }
                }
            }
        } catch (Throwable ignored) {
            // never break the application
        }
    }

    private static byte[] readBody(Object request) {
        try {
            Object stream = request.getClass().getMethod("getInputStream").invoke(request);
            if (stream == null) {
                return new byte[0];
            }
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            byte[] scratch = new byte[1024];
            int max = Capture.maxBytes();
            int n;
            java.lang.reflect.Method read = stream.getClass().getMethod("read", byte[].class);
            while (buf.size() < max && (n = (Integer) read.invoke(stream, scratch)) != -1) {
                int take = Math.min(n, max - buf.size());
                buf.write(scratch, 0, take);
                if (take < n) {
                    break;
                }
            }
            return buf.toByteArray();
        } catch (Throwable ignored) {
            return new byte[0];
        }
    }

    private static Field fieldNamed(Class<?> type, String name) {
        Class<?> cursor = type;
        while (cursor != null && cursor != Object.class) {
            try {
                return cursor.getDeclaredField(name);
            } catch (NoSuchFieldException ignored) {
                cursor = cursor.getSuperclass();
            }
        }
        return null;
    }

    private static Classes classes(ClassLoader cl, Object sample) {
        if (cl == null) {
            return null;
        }
        Classes existing = CACHE.get(cl);
        if (existing != null) {
            return existing;
        }
        synchronized (CACHE) {
            existing = CACHE.get(cl);
            if (existing != null) {
                return existing;
            }
            String pkg = servletPackage(sample);
            try {
                Classes built = generate(cl, pkg);
                CACHE.put(cl, built);
                return built;
            } catch (Throwable ignored) {
                return null;
            }
        }
    }

    private static String servletPackage(Object obj) {
        String name = obj.getClass().getName();
        if (name.startsWith("jakarta.")) {
            return "jakarta.servlet";
        }
        return "javax.servlet";
    }

    private static Classes generate(ClassLoader cl, String pkg) throws Exception {
        Class<?> reqIface = Class.forName(pkg + ".http.HttpServletRequest", true, cl);
        Class<?> resIface = Class.forName(pkg + ".http.HttpServletResponse", true, cl);
        Class<?> reqWrap = Class.forName(pkg + ".http.HttpServletRequestWrapper", true, cl);
        Class<?> resWrap = Class.forName(pkg + ".http.HttpServletResponseWrapper", true, cl);
        Class<?> servletIn = Class.forName(pkg + ".ServletInputStream", true, cl);
        Class<?> servletOut = Class.forName(pkg + ".ServletOutputStream", true, cl);

        Class<?> replayIn = new ByteBuddy()
            .subclass(servletIn)
            .name("net.cloudraft.apm.httpcapture.dynamic.ReplayServletInputStream")
            .defineField("in", ByteArrayInputStream.class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .method(ElementMatchers.named("read").and(ElementMatchers.takesArguments(0)))
            .intercept(MethodDelegation.to(ReadInterceptor.class))
            .method(ElementMatchers.named("read").and(ElementMatchers.takesArguments(byte[].class)))
            .intercept(MethodDelegation.to(ReadInterceptor.class))
            .method(ElementMatchers.named("read").and(ElementMatchers.takesArguments(byte[].class, int.class, int.class)))
            .intercept(MethodDelegation.to(ReadInterceptor.class))
            .method(ElementMatchers.named("isFinished"))
            .intercept(MethodDelegation.to(ReadInterceptor.class))
            .method(ElementMatchers.named("isReady"))
            .intercept(MethodDelegation.to(ReadInterceptor.class))
            .method(ElementMatchers.named("setReadListener"))
            .intercept(MethodDelegation.to(ReadInterceptor.class))
            .make()
            .load(cl, ClassLoadingStrategy.Default.INJECTION)
            .getLoaded();

        Class<?> requestClass = new ByteBuddy()
            .subclass(reqWrap)
            .name("net.cloudraft.apm.httpcapture.dynamic.CachedHttpServletRequest")
            .defineField("crnetBody", byte[].class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .defineField("crnetReplayClass", Class.class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .method(ElementMatchers.named("getInputStream"))
            .intercept(MethodDelegation.to(RequestInterceptor.class))
            .make()
            .load(cl, ClassLoadingStrategy.Default.INJECTION)
            .getLoaded();

        Class<?> teeOut = new ByteBuddy()
            .subclass(servletOut)
            .name("net.cloudraft.apm.httpcapture.dynamic.TeeServletOutputStream")
            .defineField("out", OutputStream.class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .method(ElementMatchers.named("write").and(ElementMatchers.takesArguments(int.class)))
            .intercept(MethodDelegation.to(WriteInterceptor.class))
            .method(ElementMatchers.named("write").and(ElementMatchers.takesArguments(byte[].class)))
            .intercept(MethodDelegation.to(WriteInterceptor.class))
            .method(ElementMatchers.named("write").and(ElementMatchers.takesArguments(byte[].class, int.class, int.class)))
            .intercept(MethodDelegation.to(WriteInterceptor.class))
            .method(ElementMatchers.named("isReady"))
            .intercept(MethodDelegation.to(WriteInterceptor.class))
            .method(ElementMatchers.named("setWriteListener"))
            .intercept(MethodDelegation.to(WriteInterceptor.class))
            .make()
            .load(cl, ClassLoadingStrategy.Default.INJECTION)
            .getLoaded();

        Class<?> responseClass = new ByteBuddy()
            .subclass(resWrap)
            .name("net.cloudraft.apm.httpcapture.dynamic.CachedHttpServletResponse")
            .defineField("crnetBuffer", ByteArrayOutputStream.class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .defineField("crnetStream", Object.class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .defineField("crnetTeeClass", Class.class, net.bytebuddy.description.modifier.Visibility.PUBLIC)
            .method(ElementMatchers.named("getOutputStream"))
            .intercept(MethodDelegation.to(ResponseInterceptor.class))
            .make()
            .load(cl, ClassLoadingStrategy.Default.INJECTION)
            .getLoaded();

        Classes classes = new Classes();
        classes.requestCtor = requestClass.getConstructor(reqIface);
        classes.responseCtor = responseClass.getConstructor(resIface);
        classes.requestBody = requestClass.getField("crnetBody");
        classes.requestReplay = requestClass.getField("crnetReplayClass");
        classes.responseBuffer = responseClass.getField("crnetBuffer");
        classes.responseTee = responseClass.getField("crnetTeeClass");
        classes.replayIn = replayIn;
        classes.teeOut = teeOut;
        return classes;
    }

    static final class Classes {
        Constructor<?> requestCtor;
        Constructor<?> responseCtor;
        Field requestBody;
        Field requestReplay;
        Field responseBuffer;
        Field responseTee;
        Class<?> replayIn;
        Class<?> teeOut;
    }

    public static final class ReadInterceptor {
        @RuntimeType
        public static int read(@This Object self, @AllArguments Object[] args) throws IOException {
            ByteArrayInputStream in = field(self, "in");
            if (args == null || args.length == 0) {
                return in.read();
            }
            if (args.length == 1) {
                return in.read((byte[]) args[0]);
            }
            return in.read((byte[]) args[0], (Integer) args[1], (Integer) args[2]);
        }

        @RuntimeType
        public static boolean isFinished(@This Object self) {
            ByteArrayInputStream in = field(self, "in");
            return in.available() == 0;
        }

        @RuntimeType
        public static boolean isReady(@This Object self) {
            return true;
        }

        @RuntimeType
        public static void setReadListener(@This Object self, @AllArguments Object[] args) {
            // no-op: captured body is already fully buffered
        }

        @SuppressWarnings("unchecked")
        private static ByteArrayInputStream field(Object self, String name) {
            try {
                Field f = self.getClass().getField(name);
                return (ByteArrayInputStream) f.get(self);
            } catch (Exception e) {
                return new ByteArrayInputStream(new byte[0]);
            }
        }
    }

    public static final class RequestInterceptor {
        @RuntimeType
        public static Object getInputStream(@This Object self) throws Exception {
            byte[] body = (byte[]) self.getClass().getField("crnetBody").get(self);
            Class<?> replayClass = (Class<?>) self.getClass().getField("crnetReplayClass").get(self);
            Object stream = replayClass.getDeclaredConstructor().newInstance();
            replayClass.getField("in").set(stream, new ByteArrayInputStream(body == null ? new byte[0] : body));
            return stream;
        }
    }

    public static final class WriteInterceptor {
        @RuntimeType
        public static void write(@This Object self, @AllArguments Object[] args) throws IOException {
            OutputStream out = field(self, "out");
            if (args.length == 1 && args[0] instanceof Integer) {
                out.write((Integer) args[0]);
                return;
            }
            if (args.length == 1) {
                out.write((byte[]) args[0]);
                return;
            }
            out.write((byte[]) args[0], (Integer) args[1], (Integer) args[2]);
        }

        @RuntimeType
        public static boolean isReady(@This Object self) {
            return true;
        }

        @RuntimeType
        public static void setWriteListener(@This Object self, @AllArguments Object[] args) {
            // no-op
        }

        private static OutputStream field(Object self, String name) {
            try {
                return (OutputStream) self.getClass().getField(name).get(self);
            } catch (Exception e) {
                return new FilterOutputStream(new ByteArrayOutputStream());
            }
        }
    }

    public static final class ResponseInterceptor {
        @RuntimeType
        public static Object getOutputStream(@This Object self) throws Exception {
            Field streamField = self.getClass().getField("crnetStream");
            Object existing = streamField.get(self);
            if (existing != null) {
                return existing;
            }
            ByteArrayOutputStream buf = (ByteArrayOutputStream) self.getClass().getField("crnetBuffer").get(self);
            if (buf == null) {
                buf = new ByteArrayOutputStream();
                self.getClass().getField("crnetBuffer").set(self, buf);
            }
            Object wrapped = self.getClass().getMethod("getResponse").invoke(self);
            OutputStream original = (OutputStream) wrapped.getClass().getMethod("getOutputStream").invoke(wrapped);
            TeeOutputStream tee = new TeeOutputStream(original, buf);
            Class<?> teeClass = (Class<?>) self.getClass().getField("crnetTeeClass").get(self);
            Object stream = teeClass.getDeclaredConstructor().newInstance();
            teeClass.getField("out").set(stream, tee);
            streamField.set(self, stream);
            return stream;
        }
    }

    static final class TeeOutputStream extends FilterOutputStream {
        private final OutputStream copy;
        private final int max = Capture.maxBytes();
        private int written;

        TeeOutputStream(OutputStream out, OutputStream copy) {
            super(out);
            this.copy = copy;
        }

        @Override
        public void write(int b) throws IOException {
            out.write(b);
            if (written < max) {
                copy.write(b);
                written++;
            }
        }

        @Override
        public void write(byte[] b, int off, int len) throws IOException {
            out.write(b, off, len);
            int take = Math.min(len, Math.max(0, max - written));
            if (take > 0) {
                copy.write(b, off, take);
                written += take;
            }
        }
    }
}
