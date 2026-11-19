#ifndef MY_LIVEHUB_LXQJS_H
#define MY_LIVEHUB_LXQJS_H

#include <stddef.h>
#include "quickjs.h"

typedef struct LXQJSState LXQJSState;

LXQJSState *lxqjs_new(size_t memory_limit, size_t stack_limit, char **error_out);
void lxqjs_free(LXQJSState *state);
void lxqjs_set_interrupted(LXQJSState *state, int interrupted);
char *lxqjs_eval(LXQJSState *state, const char *source, const char *filename, char **error_out);
int lxqjs_compile(LXQJSState *state, const char *source, const char *filename, char **error_out);
void lxqjs_string_free(char *value);
int lxqjs_install_crypto(LXQJSState *state, char **error_out);

#endif
