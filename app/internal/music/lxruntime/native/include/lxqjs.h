#ifndef MY_LIVEHUB_LXQJS_H
#define MY_LIVEHUB_LXQJS_H

#include <stddef.h>
#include "quickjs.h"

#if defined(_WIN32) && defined(LXQJS_BUILD)
#define LXQJS_API __declspec(dllexport)
#elif defined(_WIN32)
#define LXQJS_API __declspec(dllimport)
#else
#define LXQJS_API __attribute__((visibility("default")))
#endif

typedef struct LXQJSState LXQJSState;

LXQJS_API LXQJSState *lxqjs_new(size_t memory_limit, size_t stack_limit, char **error_out);
LXQJS_API void lxqjs_free(LXQJSState *state);
LXQJS_API void lxqjs_set_interrupted(LXQJSState *state, int interrupted);
LXQJS_API char *lxqjs_eval(LXQJSState *state, const char *source, const char *filename, char **error_out);
LXQJS_API char *lxqjs_eval_timed(LXQJSState *state, const char *source, const char *filename, char **error_out, double *compile_ms, double *execute_ms, double *jobs_ms);
LXQJS_API int lxqjs_compile(LXQJSState *state, const char *source, const char *filename, char **error_out);
LXQJS_API void lxqjs_string_free(char *value);
LXQJS_API int lxqjs_install_crypto(LXQJSState *state, char **error_out);

typedef void *(*LXQJSAllocFn)(size_t);
LXQJS_API void *lxqjs_alloc(size_t size);
LXQJS_API void lxqjs_dealloc(void *value);
LXQJS_API void lxqjs_set_host_callbacks(void *md5, void *random_bytes, void *buffer_from,
    void *buffer_to_string, void *rsa_encrypt, void *aes_encrypt, void *zlib);

#endif
