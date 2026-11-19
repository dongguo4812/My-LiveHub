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
extern void *lxqjsGoRandomBytes(size_t length);
extern void *lxqjsGoAESEncrypt(const char *data, size_t data_len, const char *mode, const char *key, size_t key_len, const char *iv, size_t iv_len, size_t *out_len);

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

static JSValue lxqjs_random_bytes_callback(JSContext *context, JSValueConst this_val,
                                           int argc, JSValueConst *argv) {
    (void)this_val;
    uint64_t length = 0;
    if (argc < 1 || JS_ToIndex(context, &length, argv[0]) < 0)
        return JS_ThrowTypeError(context, "randomBytes requires a valid size");
    if (length > (1u << 20))
        return JS_ThrowRangeError(context, "randomBytes size exceeds 1 MiB");
    if (length == 0) return JS_NewUint8ArrayCopy(context, NULL, 0);
    void *bytes = lxqjsGoRandomBytes((size_t)length);
    if (bytes == NULL) return JS_ThrowInternalError(context, "randomBytes failed");
    JSValue result = JS_NewUint8ArrayCopy(context, (const uint8_t *)bytes, (size_t)length);
    free(bytes);
    return result;
}


static int lxqjs_copy_bytes(JSContext *context, JSValueConst value, uint8_t **out, size_t *out_len, int allow_undefined) {
    *out = NULL; *out_len = 0;
    if (allow_undefined && JS_IsUndefined(value)) return 0;
    if (JS_IsString(value)) {
        size_t length = 0;
        const char *text = JS_ToCStringLen(context, &length, value);
        if (text == NULL) return -1;
        uint8_t *copy = (uint8_t *)malloc(length ? length : 1);
        if (copy == NULL) { JS_FreeCString(context, text); return -1; }
        if (length) memcpy(copy, text, length);
        JS_FreeCString(context, text);
        *out = copy; *out_len = length;
        return 0;
    }
    if (JS_IsArrayBuffer(value)) {
        size_t length = 0;
        uint8_t *data = JS_GetArrayBuffer(context, &length, value);
        if (data == NULL && length) return -1;
        uint8_t *copy = (uint8_t *)malloc(length ? length : 1);
        if (copy == NULL) return -1;
        if (length) memcpy(copy, data, length);
        *out = copy; *out_len = length;
        return 0;
    }
    if (JS_GetTypedArrayType(value) >= 0) {
        size_t length = 0;
        uint8_t *data = JS_GetUint8Array(context, &length, value);
        if (data == NULL && length) return -1;
        uint8_t *copy = (uint8_t *)malloc(length ? length : 1);
        if (copy == NULL) return -1;
        if (length) memcpy(copy, data, length);
        *out = copy; *out_len = length;
        return 0;
    }
    JS_ThrowTypeError(context, "expected a string, ArrayBuffer, or Uint8Array");
    return -1;
}

static JSValue lxqjs_aes_encrypt_callback(JSContext *context, JSValueConst this_val,
                                          int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 4) return JS_ThrowTypeError(context, "aesEncrypt requires buffer, mode, key, and iv");
    const char *mode = JS_ToCString(context, argv[1]);
    if (mode == NULL) return JS_EXCEPTION;
    uint8_t *data = NULL, *key = NULL, *iv = NULL;
    size_t data_len = 0, key_len = 0, iv_len = 0;
    int failed = lxqjs_copy_bytes(context, argv[0], &data, &data_len, 0) ||
                 lxqjs_copy_bytes(context, argv[2], &key, &key_len, 0) ||
                 lxqjs_copy_bytes(context, argv[3], &iv, &iv_len, 1);
    if (failed || data_len > (1u << 20) || key_len > 64 || iv_len > 32) {
        free(data); free(key); free(iv); JS_FreeCString(context, mode);
        return JS_ThrowTypeError(context, "invalid AES input or input exceeds limit");
    }
    size_t output_len = 0;
    void *output = lxqjsGoAESEncrypt((const char *)data, data_len, mode, (const char *)key, key_len,
                                     (const char *)iv, iv_len, &output_len);
    free(data); free(key); free(iv); JS_FreeCString(context, mode);
    if (output == NULL) return JS_ThrowTypeError(context, "AES encryption failed or mode is unsupported");
    JSValue result = JS_NewUint8ArrayCopy(context, (const uint8_t *)output, output_len);
    free(output);
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
    if (result >= 0) {
        JSValue aes_encrypt = JS_NewCFunction(context, lxqjs_aes_encrypt_callback, "aesEncrypt", 4);
        result = JS_SetPropertyStr(context, crypto_object, "aesEncrypt", aes_encrypt);
    }
    if (result >= 0) {
        JSValue random_bytes = JS_NewCFunction(context, lxqjs_random_bytes_callback, "randomBytes", 1);
        result = JS_SetPropertyStr(context, crypto_object, "randomBytes", random_bytes);
    }
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
