package net.cloudraft.apm.agent;

import java.io.InputStream;
import java.lang.instrument.Instrumentation;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.jar.JarFile;

/**
 * CRNET APM Java agent. The operator copies this file as /javaagent.jar into
 * every instrumented JVM. It starts the official auto-instrumentation agent
 * first, then the HTTP body-capture agent.
 */
public final class CrnetAgent {
    private CrnetAgent() {}

    public static void premain(String args, Instrumentation inst) {
        agentmain(args, inst);
    }

    public static void agentmain(String args, Instrumentation inst) {
        try {
            runNested(inst, "otel-javaagent.jar", "io.opentelemetry.javaagent.OpenTelemetryAgent", args, true);
        } catch (Throwable t) {
            System.err.println("CRNET APM: failed to start tracing agent: " + t.getMessage());
        }
        try {
            runNested(inst, "crnet-http-capture.jar", "net.cloudraft.apm.httpcapture.Agent", args, false);
        } catch (Throwable t) {
            System.err.println("CRNET APM: failed to start HTTP capture agent: " + t.getMessage());
        }
    }

    private static void runNested(
        Instrumentation inst,
        String resource,
        String premainClass,
        String args,
        boolean bootstrap
    ) throws Exception {
        Path tmp = Files.createTempFile("crnet-", ".jar");
        tmp.toFile().deleteOnExit();
        try (InputStream in = CrnetAgent.class.getClassLoader().getResourceAsStream(resource)) {
            if (in == null) {
                throw new IllegalStateException("missing nested agent " + resource);
            }
            Files.copy(in, tmp, StandardCopyOption.REPLACE_EXISTING);
        }
        JarFile jar = new JarFile(tmp.toFile());
        if (bootstrap) {
            inst.appendToBootstrapClassLoaderSearch(jar);
        } else {
            inst.appendToSystemClassLoaderSearch(jar);
        }
        ClassLoader loader = bootstrap ? null : ClassLoader.getSystemClassLoader();
        Class<?> cls = Class.forName(premainClass, true, loader);
        cls.getMethod("premain", String.class, Instrumentation.class).invoke(null, args, inst);
    }
}
