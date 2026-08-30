package net.cloudraft.apm.httpcapture;

import net.bytebuddy.agent.builder.AgentBuilder;
import net.bytebuddy.asm.Advice;
import net.bytebuddy.description.type.TypeDescription;
import net.bytebuddy.dynamic.DynamicType;
import net.bytebuddy.matcher.ElementMatchers;
import net.bytebuddy.utility.JavaModule;

import java.lang.instrument.Instrumentation;
import java.security.ProtectionDomain;

public final class Agent {
    private Agent() {}

    public static void premain(String args, Instrumentation inst) {
        agentmain(args, inst);
    }

    public static void agentmain(String args, Instrumentation inst) {
        if (!Capture.enabled()) {
            return;
        }
        new AgentBuilder.Default()
            .ignore(ElementMatchers.nameStartsWith("net.bytebuddy."))
            .ignore(ElementMatchers.nameStartsWith("net.cloudraft.apm.httpcapture."))
            .with(AgentBuilder.RedefinitionStrategy.RETRANSFORMATION)
            .type(ElementMatchers.named("javax.servlet.http.HttpServlet")
                .or(ElementMatchers.named("jakarta.servlet.http.HttpServlet"))
                .or(ElementMatchers.named("org.springframework.web.servlet.DispatcherServlet")))
            .transform(new ServletTransformer())
            .installOn(inst);
    }

    static final class ServletTransformer implements AgentBuilder.Transformer {
        @Override
        public DynamicType.Builder<?> transform(
            DynamicType.Builder<?> builder,
            TypeDescription typeDescription,
            ClassLoader classLoader,
            JavaModule module,
            ProtectionDomain protectionDomain
        ) {
            String name = typeDescription.getName();
            if (name.endsWith("DispatcherServlet")) {
                return builder.visit(Advice.to(SpringAdvice.class).on(
                    ElementMatchers.named("doDispatch").and(ElementMatchers.takesArguments(2))));
            }
            return builder.visit(Advice.to(ServletAdvice.class).on(
                ElementMatchers.named("service").and(ElementMatchers.takesArguments(2))));
        }
    }
}
