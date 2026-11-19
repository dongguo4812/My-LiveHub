#include <stdio.h>
#include <string.h>

#include "quickjs.h"

int main(void) {
    JSRuntime *rt = JS_NewRuntime();
    if (rt == NULL) {
        fputs("JS_NewRuntime failed\n", stderr);
        return 1;
    }

    JS_SetMemoryLimit(rt, 16 * 1024 * 1024);
    JS_SetMaxStackSize(rt, 512 * 1024);

    JSContext *ctx = JS_NewContext(rt);
    if (ctx == NULL) {
        fputs("JS_NewContext failed\n", stderr);
        JS_FreeRuntime(rt);
        return 1;
    }

    const char *source = "(() => { const value = new Uint8Array([40, 2]); return value[0] + value[1]; })()";
    JSValue result = JS_Eval(ctx, source, strlen(source), "lx-quickjs-poc.js", JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(result)) {
        JSValue exception = JS_GetException(ctx);
        const char *message = JS_ToCString(ctx, exception);
        fprintf(stderr, "JS_Eval failed: %s\n", message != NULL ? message : "(unknown exception)");
        if (message != NULL) JS_FreeCString(ctx, message);
        JS_FreeValue(ctx, exception);
        JS_FreeValue(ctx, result);
        JS_FreeContext(ctx);
        JS_FreeRuntime(rt);
        return 1;
    }

    int32_t value = 0;
    int conversion = JS_ToInt32(ctx, &value, result);
    JS_FreeValue(ctx, result);
    JS_FreeContext(ctx);
    JS_FreeRuntime(rt);

    if (conversion < 0 || value != 42) {
        fprintf(stderr, "unexpected JavaScript result: %ld\n", (long)value);
        return 1;
    }

    puts("QuickJS-NG C API embedding smoke test passed");
    return 0;
}
