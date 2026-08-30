package net.cloudraft.apm.httpcapture;

import net.bytebuddy.asm.Advice;

public final class ServletAdvice {
    private ServletAdvice() {}

    @Advice.OnMethodEnter(suppress = Throwable.class)
    public static Object[] onEnter(
        @Advice.Argument(value = 0, readOnly = false) Object request,
        @Advice.Argument(value = 1, readOnly = false) Object response
    ) {
        Object wrappedReq = Capture.wrapGenericRequest(request);
        Object wrappedRes = Capture.wrapGenericResponse(response);
        if (wrappedReq != null) {
            request = wrappedReq;
        } else {
            Capture.recordRequest(request);
        }
        if (wrappedRes != null) {
            response = wrappedRes;
        }
        return new Object[] { request, response };
    }

    @Advice.OnMethodExit(suppress = Throwable.class, onThrowable = Throwable.class)
    public static void onExit(@Advice.Enter Object[] wrapped) {
        if (wrapped == null) {
            return;
        }
        Capture.flushGeneric(wrapped[0], wrapped[1]);
    }
}
