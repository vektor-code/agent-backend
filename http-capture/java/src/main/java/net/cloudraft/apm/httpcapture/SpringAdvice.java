package net.cloudraft.apm.httpcapture;

import net.bytebuddy.asm.Advice;

public final class SpringAdvice {
    private SpringAdvice() {}

    @Advice.OnMethodEnter(suppress = Throwable.class)
    public static Object[] onEnter(
        @Advice.Argument(value = 0, readOnly = false) Object request,
        @Advice.Argument(value = 1, readOnly = false) Object response
    ) {
        Object wrappedReq = Capture.wrapSpringRequest(request);
        Object wrappedRes = Capture.wrapSpringResponse(response);
        if (wrappedReq != null) {
            request = wrappedReq;
        }
        if (wrappedRes != null) {
            response = wrappedRes;
        }
        return new Object[] { wrappedReq, wrappedRes };
    }

    @Advice.OnMethodExit(suppress = Throwable.class, onThrowable = Throwable.class)
    public static void onExit(@Advice.Enter Object[] wrapped) {
        if (wrapped == null) {
            return;
        }
        Capture.flushSpring(wrapped[0], wrapped[1]);
    }
}
