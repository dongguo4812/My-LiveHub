//go:build lxquickjs

#include "lxqjs.h"

#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct LXQJSState {
    JSRuntime *runtime;
    JSContext *context;
    atomic_int interrupted;
};

static char *lxqjs_copy_string(const char *value) {
    if (value == NULL) value = "unknown QuickJS error";
    size_t length = strlen(value);
    char *copy = (char *)malloc(length + 1);
    if (copy != NULL) memcpy(copy, value, length + 1);
    return copy;
}

static void lxqjs_set_error(char **error_out, const char *message) {
    if (error_out != NULL) *error_out = lxqjs_copy_string(message);
}

static int lxqjs_interrupt_handler(JSContext *context, void *opaque) {
    (void)context;
    LXQJSState *state = (LXQJSState *)opaque;
    return atomic_load_explicit(&state->interrupted, memory_order_relaxed) != 0;
}

LXQJSState *lxqjs_new(size_t memory_limit, size_t stack_limit, char **error_out) {
    if (error_out != NULL) *error_out = NULL;
    LXQJSState *state = (LXQJSState *)calloc(1, sizeof(LXQJSState));
    if (state == NULL) {
        lxqjs_set_error(error_out, "unable to allocate QuickJS state");
        return NULL;
    }
    atomic_init(&state->interrupted, 0);
    state->runtime = JS_NewRuntime();
    if (state->runtime == NULL) {
        lxqjs_set_error(error_out, "JS_NewRuntime failed");
        free(state);
        return NULL;
    }
    JS_SetMemoryLimit(state->runtime, memory_limit);
    JS_SetMaxStackSize(state->runtime, stack_limit);
    JS_SetInterruptHandler(state->runtime, lxqjs_interrupt_handler, state);
    state->context = JS_NewContext(state->runtime);
    if (state->context == NULL) {
        lxqjs_set_error(error_out, "JS_NewContext failed");
        JS_FreeRuntime(state->runtime);
        free(state);
        return NULL;
    }
    return state;
}

void lxqjs_free(LXQJSState *state) {
    if (state == NULL) return;
    if (state->context != NULL) JS_FreeContext(state->context);
    if (state->runtime != NULL) JS_FreeRuntime(state->runtime);
    free(state);
}

void lxqjs_set_interrupted(LXQJSState *state, int interrupted) {
    if (state != NULL) {
        atomic_store_explicit(&state->interrupted, interrupted ? 1 : 0, memory_order_relaxed);
    }
}

static char *lxqjs_exception_text(JSContext *context) {
    JSValue exception = JS_GetException(context);
    const char *message = JS_ToCString(context, exception);
    char *copy = lxqjs_copy_string(message != NULL ? message : "JavaScript exception");
    if (message != NULL) JS_FreeCString(context, message);
    JS_FreeValue(context, exception);
    return copy;
}

char *lxqjs_eval(LXQJSState *state, const char *source, const char *filename, char **error_out) {
    if (error_out != NULL) *error_out = NULL;
    if (state == NULL || source == NULL) {
        lxqjs_set_error(error_out, "invalid QuickJS eval arguments");
        return NULL;
    }

    JSValue result = JS_Eval(
        state->context,
        source,
        strlen(source),
        filename != NULL ? filename : "lx-source.js",
        JS_EVAL_TYPE_GLOBAL
    );
    if (JS_IsException(result)) {
        if (error_out != NULL) *error_out = lxqjs_exception_text(state->context);
        JS_FreeValue(state->context, result);
        return NULL;
    }

    unsigned int jobs = 0;
    while (JS_IsJobPending(state->runtime)) {
        JSContext *job_context = NULL;
        int job_result = JS_ExecutePendingJob(state->runtime, &job_context);
        if (job_result < 0) {
            if (error_out != NULL) *error_out = lxqjs_exception_text(job_context != NULL ? job_context : state->context);
            JS_FreeValue(state->context, result);
            return NULL;
        }
        if (++jobs > 1000) {
            lxqjs_set_error(error_out, "QuickJS pending-job limit exceeded");
            JS_FreeValue(state->context, result);
            return NULL;
        }
    }

    const char *text = JS_ToCString(state->context, result);
    char *copy = lxqjs_copy_string(text != NULL ? text : "undefined");
    if (text != NULL) JS_FreeCString(state->context, text);
    JS_FreeValue(state->context, result);
    if (copy == NULL) lxqjs_set_error(error_out, "unable to allocate result string");
    return copy;
}

int lxqjs_compile(LXQJSState *state, const char *source, const char *filename, char **error_out) {
    if (error_out != NULL) *error_out = NULL;
    if (state == NULL || source == NULL) {
        lxqjs_set_error(error_out, "invalid QuickJS compile arguments");
        return -1;
    }
    JSValue compiled = JS_Eval(
        state->context,
        source,
        strlen(source),
        filename != NULL ? filename : "lx-source.js",
        JS_EVAL_TYPE_GLOBAL | JS_EVAL_FLAG_COMPILE_ONLY
    );
    if (JS_IsException(compiled)) {
        if (error_out != NULL) *error_out = lxqjs_exception_text(state->context);
        JS_FreeValue(state->context, compiled);
        return -1;
    }
    JS_FreeValue(state->context, compiled);
    return 0;
}

void lxqjs_string_free(char *value) {
    free(value);
}


/* Synchronous Go-backed primitives. The callback executes on the QuickJS
 * owner thread; the exported Go function performs only bounded pure work. */
extern char *lxqjsGoMD5(const char *data, size_t length);

static JSValue lxqjs_md5_callback(JSContext *context, JSValueConst this_val,
                                  int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(context, "md5 requires a value");
    size_t length = 0;
    const char *input = JS_ToCStringLen(context, &length, argv[0]);
    if (input == NULL) return JS_EXCEPTION;
    char *digest = lxqjsGoMD5(input, length);
    JS_FreeCString(context, input);
    if (digest == NULL) return JS_ThrowInternalError(context, "md5 failed");
    JSValue result = JS_NewString(context, digest);
    free(digest);
    return result;
}

int lxqjs_install_crypto(LXQJSState *state, char **error_out) {
    if (error_out != NULL) *error_out = NULL;
    if (state == NULL || state->context == NULL) {
        lxqjs_set_error(error_out, "invalid QuickJS state for crypto installation");
        return -1;
    }
    JSContext *context = state->context;
    JSValue lx = JS_GetGlobalObject(context);
    JSValue utils = JS_GetPropertyStr(context, lx, "lx");
    JSValue crypto = JS_GetPropertyStr(context, utils, "utils");
    JSValue crypto_object = JS_GetPropertyStr(context, crypto, "crypto");
    if (JS_IsException(crypto_object) || JS_IsUndefined(crypto_object)) {
        lxqjs_set_error(error_out, "LX crypto host object is unavailable");
        JS_FreeValue(context, crypto_object);
        JS_FreeValue(context, crypto);
        JS_FreeValue(context, utils);
        JS_FreeValue(context, lx);
        return -1;
    }
    JSValue md5 = JS_NewCFunction(context, lxqjs_md5_callback, "md5", 1);
    int result = JS_SetPropertyStr(context, crypto_object, "md5", md5);
    JS_FreeValue(context, crypto_object);
    JS_FreeValue(context, crypto);
    JS_FreeValue(context, utils);
    JS_FreeValue(context, lx);
    if (result < 0) {
        lxqjs_set_error(error_out, "unable to install LX crypto.md5");
        return -1;
    }
    return 0;
}
