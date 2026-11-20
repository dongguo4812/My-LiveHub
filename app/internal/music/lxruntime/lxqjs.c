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
extern void *lxqjsGoBufferFrom(const char *data, size_t data_len, const char *encoding, size_t *out_len);
extern void *lxqjsGoBufferToString(const char *data, size_t data_len, const char *encoding, size_t *out_len);
extern void *lxqjsGoRSAEncrypt(const char *data, size_t data_len, const char *key, size_t key_len, size_t *out_len);
extern void *lxqjsGoAESEncrypt(const char *data, size_t data_len, const char *mode, const char *key, size_t key_len, const char *iv, size_t iv_len, size_t *out_len);
extern void *lxqjsGoZlib(const char *data, size_t data_len, int inflate, size_t *out_len, char **error_out);

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

static JSValue lxqjs_rsa_encrypt_callback(JSContext *context, JSValueConst this_val,
                                          int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 2) return JS_ThrowTypeError(context, "rsaEncrypt requires buffer and public key");
    uint8_t *data = NULL, *key = NULL;
    size_t data_len = 0, key_len = 0;
    if (lxqjs_copy_bytes(context, argv[0], &data, &data_len, 0) < 0 ||
        lxqjs_copy_bytes(context, argv[1], &key, &key_len, 0) < 0) {
        free(data); free(key);
        return JS_EXCEPTION;
    }
    if (data_len > 128 || key_len == 0 || key_len > (1u << 20)) {
        free(data); free(key);
        return JS_ThrowRangeError(context, "invalid RSA input or public key size");
    }
    size_t output_len = 0;
    void *output = lxqjsGoRSAEncrypt((const char *)data, data_len,
                                     (const char *)key, key_len, &output_len);
    free(data); free(key);
    if (output == NULL) return JS_ThrowTypeError(context, "RSA encryption failed: invalid key or input");
    JSValue result = JS_NewUint8ArrayCopy(context, (const uint8_t *)output, output_len);
    free(output);
    return result;
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


static JSValue lxqjs_buffer_from_callback(JSContext *context, JSValueConst this_val,
                                           int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(context, "buffer.from expects an input");
    uint8_t *input = NULL;
    size_t input_len = 0;
    const char *encoding = "utf8";
    int owns_encoding = 0;
    if (JS_IsString(argv[0])) {
        const char *text = JS_ToCStringLen(context, &input_len, argv[0]);
        if (text == NULL) return JS_EXCEPTION;
        input = (uint8_t *)malloc(input_len ? input_len : 1);
        if (input == NULL) { JS_FreeCString(context, text); return JS_ThrowOutOfMemory(context); }
        if (input_len) memcpy(input, text, input_len);
        JS_FreeCString(context, text);
        if (argc > 1 && !JS_IsUndefined(argv[1])) {
            encoding = JS_ToCString(context, argv[1]);
            if (encoding == NULL) { free(input); return JS_EXCEPTION; }
            owns_encoding = 1;
        }
    } else if (JS_IsArray(argv[0])) {
        JSValue length_value = JS_GetPropertyStr(context, argv[0], "length");
        uint64_t length = 0;
        if (JS_IsException(length_value) || JS_ToIndex(context, &length, length_value) < 0 || length > (8u << 20)) {
            JS_FreeValue(context, length_value);
            return JS_ThrowRangeError(context, "Buffer.from array length is invalid or exceeds 8 MiB");
        }
        JS_FreeValue(context, length_value);
        input_len = (size_t)length;
        input = (uint8_t *)malloc(input_len ? input_len : 1);
        if (input == NULL) return JS_ThrowOutOfMemory(context);
        for (uint32_t i = 0; i < (uint32_t)input_len; i++) {
            JSValue item = JS_GetPropertyUint32(context, argv[0], i);
            int32_t number = 0;
            if (JS_IsException(item) || JS_ToInt32(context, &number, item) < 0) {
                JS_FreeValue(context, item); free(input); return JS_EXCEPTION;
            }
            JS_FreeValue(context, item);
            input[i] = (uint8_t)number;
        }
    } else if (lxqjs_copy_bytes(context, argv[0], &input, &input_len, 0) < 0) {
        return JS_EXCEPTION;
    }
    size_t output_len = 0;
    void *output = lxqjsGoBufferFrom((const char *)input, input_len, encoding, &output_len);
    if (owns_encoding) JS_FreeCString(context, encoding);
    free(input);
    if (output == NULL) return JS_ThrowTypeError(context, "unsupported buffer encoding or invalid input");
    JSValue result = JS_NewUint8ArrayCopy(context, (const uint8_t *)output, output_len);
    free(output);
    return result;
}

static JSValue lxqjs_buffer_to_string_callback(JSContext *context, JSValueConst this_val,
                                                int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(context, "bufToString expects a byte buffer");
    uint8_t *input = NULL;
    size_t input_len = 0;
    if (lxqjs_copy_bytes(context, argv[0], &input, &input_len, 0) < 0) return JS_EXCEPTION;
    const char *encoding = "utf8";
    if (argc > 1 && !JS_IsUndefined(argv[1])) {
        encoding = JS_ToCString(context, argv[1]);
        if (encoding == NULL) { free(input); return JS_EXCEPTION; }
    }
    size_t output_len = 0;
    void *output = lxqjsGoBufferToString((const char *)input, input_len, encoding, &output_len);
    if (argc > 1 && !JS_IsUndefined(argv[1])) JS_FreeCString(context, encoding);
    free(input);
    if (output == NULL) return JS_ThrowTypeError(context, "unsupported buffer encoding");
    JSValue result = JS_NewStringLen(context, (const char *)output, output_len);
    free(output);
    return result;
}

static JSValue lxqjs_zlib_callback(JSContext *context, JSValueConst this_val,
                                   int argc, JSValueConst *argv, int inflate) {
    (void)this_val;
    JSValue resolving[2];
    JSValue promise = JS_NewPromiseCapability(context, resolving);
    if (JS_IsException(promise)) return promise;
    uint8_t *input = NULL;
    size_t input_len = 0, output_len = 0;
    char *host_error = NULL;
    void *output = NULL;
    if (argc < 1) {
        JS_ThrowTypeError(context, "zlib operation requires a byte buffer");
        JSValue reason = JS_GetException(context);
        JS_Call(context, resolving[1], JS_UNDEFINED, 1, &reason);
        JS_FreeValue(context, reason);
    } else if (lxqjs_copy_bytes(context, argv[0], &input, &input_len, 0) < 0) {
        JSValue reason = JS_GetException(context);
        JS_Call(context, resolving[1], JS_UNDEFINED, 1, &reason);
        JS_FreeValue(context, reason);
    } else if (input_len > (8u << 20)) {
        JSValue reason = JS_NewError(context);
        JS_SetPropertyStr(context, reason, "message", JS_NewString(context, "zlib input exceeds 8388608 bytes"));
        JS_Call(context, resolving[1], JS_UNDEFINED, 1, &reason);
        JS_FreeValue(context, reason);
    } else {
        output = lxqjsGoZlib((const char *)input, input_len, inflate, &output_len, &host_error);
        if (output == NULL) {
            JSValue reason = JS_NewError(context);
            JS_SetPropertyStr(context, reason, "message",
                JS_NewString(context, host_error != NULL ? host_error : "zlib operation failed"));
            JS_Call(context, resolving[1], JS_UNDEFINED, 1, &reason);
            JS_FreeValue(context, reason);
        } else {
            JSValue result = JS_NewUint8ArrayCopy(context, (const uint8_t *)output, output_len);
            JS_Call(context, resolving[0], JS_UNDEFINED, 1, &result);
            JS_FreeValue(context, result);
        }
    }
    free(input);
    free(output);
    free(host_error);
    JS_FreeValue(context, resolving[0]);
    JS_FreeValue(context, resolving[1]);
    return promise;
}
static JSValue lxqjs_zlib_inflate_callback(JSContext *context, JSValueConst this_val, int argc, JSValueConst *argv) {
    return lxqjs_zlib_callback(context, this_val, argc, argv, 1);
}
static JSValue lxqjs_zlib_deflate_callback(JSContext *context, JSValueConst this_val, int argc, JSValueConst *argv) {
    return lxqjs_zlib_callback(context, this_val, argc, argv, 0);
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
    JSValue lx_utils = JS_GetPropertyStr(context, utils, "utils");
    JSValue crypto_object = JS_GetPropertyStr(context, lx_utils, "crypto");
    if (JS_IsException(crypto_object) || JS_IsUndefined(crypto_object)) {
        lxqjs_set_error(error_out, "LX crypto host object is unavailable");
        JS_FreeValue(context, crypto_object);
        JS_FreeValue(context, lx_utils);
        JS_FreeValue(context, utils);
        JS_FreeValue(context, lx);
        return -1;
    }
    int result = JS_SetPropertyStr(context, crypto_object, "md5",
        JS_NewCFunction(context, lxqjs_md5_callback, "md5", 1));
    if (result >= 0) result = JS_SetPropertyStr(context, crypto_object, "aesEncrypt",
        JS_NewCFunction(context, lxqjs_aes_encrypt_callback, "aesEncrypt", 4));
    if (result >= 0) result = JS_SetPropertyStr(context, crypto_object, "rsaEncrypt",
        JS_NewCFunction(context, lxqjs_rsa_encrypt_callback, "rsaEncrypt", 2));
    if (result >= 0) result = JS_SetPropertyStr(context, crypto_object, "randomBytes",
        JS_NewCFunction(context, lxqjs_random_bytes_callback, "randomBytes", 1));


    JSValue buffer_object = JS_GetPropertyStr(context, lx_utils, "buffer");
    if (result >= 0 && !JS_IsException(buffer_object) && !JS_IsUndefined(buffer_object)) {
        result = JS_SetPropertyStr(context, buffer_object, "from",
            JS_NewCFunction(context, lxqjs_buffer_from_callback, "from", 2));
        if (result >= 0) result = JS_SetPropertyStr(context, buffer_object, "bufToString",
            JS_NewCFunction(context, lxqjs_buffer_to_string_callback, "bufToString", 2));
    } else if (result >= 0) {
        result = -1;
    }
    JS_FreeValue(context, buffer_object);

    JSValue zlib_object = JS_GetPropertyStr(context, lx_utils, "zlib");
    if (result >= 0 && !JS_IsException(zlib_object) && !JS_IsUndefined(zlib_object)) {
        result = JS_SetPropertyStr(context, zlib_object, "inflate",
            JS_NewCFunction(context, lxqjs_zlib_inflate_callback, "inflate", 1));
        if (result >= 0) result = JS_SetPropertyStr(context, zlib_object, "deflate",
            JS_NewCFunction(context, lxqjs_zlib_deflate_callback, "deflate", 1));
    } else if (result >= 0) {
        result = -1;
    }
    JS_FreeValue(context, zlib_object);

    if (result >= 0) {
        static const char buffer_shim[] =
            "(function(){"
            "class Buffer extends Uint8Array {"
            "  toString(encoding='utf8', start=0, end=this.length) {"
            "    return lx.utils.buffer.bufToString(this.subarray(start, end), encoding);"
            "  }"
            "  static from(value, encoding='utf8') {"
            "    if (typeof value === 'string') return new Buffer(lx.utils.buffer.from(value, encoding));"
            "    if (value instanceof ArrayBuffer) return new Buffer(new Uint8Array(value));"
            "    if (ArrayBuffer.isView(value)) return new Buffer(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));"
            "    if (value && typeof value.length === 'number') return new Buffer(value);"
            "    throw new TypeError('Buffer.from input must be a string, buffer, ArrayBuffer, or array-like value');"
            "  }"
            "  static alloc(size, fill=0, encoding='utf8') {"
            "    if (!Number.isInteger(size) || size < 0 || size > 1048576) throw new RangeError('Invalid buffer size');"
            "    const out = new Buffer(size);"
            "    if (typeof fill === 'number') out.fill(fill);"
            "    else if (typeof fill === 'string' || fill instanceof Uint8Array) {"
            "      const pattern = typeof fill === 'string' ? Buffer.from(fill, encoding) : Buffer.from(fill);"
            "      if (pattern.length > 0) {"
            "        for (let offset = 0; offset < size; offset += pattern.length) {"
            "          out.set(pattern.subarray(0, Math.min(pattern.length, size - offset)), offset);"
            "        }"
            "      }"
            "    } else {"
            "      throw new TypeError('The fill argument must be of type string, Buffer, Uint8Array, or number');"
            "    }"
            "    return out;"
            "  }"
            "  static concat(list, totalLength) {"
            "    if (!Array.isArray(list)) throw new TypeError('list must be an array');"
            "    const length = totalLength === undefined ? list.reduce((n, item) => n + item.length, 0) : totalLength;"
            "    const out = new Buffer(length); let offset = 0;"
            "    for (const item of list) { const part = Buffer.from(item); const count = Math.min(part.length, length-offset); if (count <= 0) break; out.set(part.subarray(0,count), offset); offset += count; }"
            "    return out;"
            "  }"
            "  static isBuffer(value) { return value instanceof Buffer; }"
            "  static byteLength(value, encoding='utf8') { return typeof value === 'string' ? lx.utils.buffer.from(value, encoding).length : value.byteLength ?? value.length; }"
            "}"
            "globalThis.Buffer = Buffer;"
            "})();";
        JSValue shim = JS_Eval(context, buffer_shim, sizeof(buffer_shim) - 1,
                               "lx-buffer-shim.js", JS_EVAL_TYPE_GLOBAL);
        if (JS_IsException(shim)) result = -1;
        JS_FreeValue(context, shim);
    }
    JS_FreeValue(context, crypto_object);
    JS_FreeValue(context, lx_utils);
    JS_FreeValue(context, utils);
    JS_FreeValue(context, lx);
    if (result < 0) {
        lxqjs_set_error(error_out, "unable to install LX crypto and buffer host APIs");
        return -1;
    }
    return 0;
}
