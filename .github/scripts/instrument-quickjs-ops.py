#!/usr/bin/env python3
"""Add opt-in numeric bytecode operand tracing to pinned QuickJS-NG v0.18.0."""
from pathlib import Path
import sys

path = Path(sys.argv[1])
source = path.read_text(encoding="utf-8")
ops = {
    "OP_add": "add", "OP_sub": "sub", "OP_mul": "mul",
    "OP_div": "div", "OP_mod": "mod", "OP_xor": "xor",
    "OP_shl": "shl", "OP_shr": "shr", "OP_sar": "sar",
    "OP_and": "and", "OP_or": "or",
}
changed = []
for opcode, label in ops.items():
    marker = f"CASE({opcode}):"
    start = source.find(marker)
    if start < 0:
        continue
    end = source.find("\n        CASE(", start + len(marker))
    if end < 0:
        end = len(source)
    block = source[start:end]
    needle = "op1 = sp[-2];\n                op2 = sp[-1];"
    if needle not in block:
        continue
    trace = f"""op1 = sp[-2];
                op2 = sp[-1];
                if (getenv("LX_QJS_TRACE_OPS") != NULL &&
                    (JS_VALUE_GET_TAG(op1) == JS_TAG_INT || JS_VALUE_GET_TAG(op1) == JS_TAG_FLOAT64) &&
                    (JS_VALUE_GET_TAG(op2) == JS_TAG_INT || JS_VALUE_GET_TAG(op2) == JS_TAG_FLOAT64) &&
                    JS_VALUE_GET_TAG(sf->cur_func) == JS_TAG_OBJECT) {{
                    JSObject *trace_obj = JS_VALUE_GET_OBJ(sf->cur_func);
                    if (js_class_has_bytecode(trace_obj->class_id)) {{
                        JSFunctionBytecode *trace_bc = trace_obj->u.func.function_bytecode;
                        const char *trace_file = JS_AtomToCString(ctx, trace_bc->filename);
                        if (trace_file != NULL && strstr(trace_file, "独家音源") != NULL) {{
                            static unsigned int trace_count;
                            if (trace_count++ < 1200) {{
                                const char *trace_fn = JS_AtomToCString(ctx, trace_bc->func_name);
                                const char *trace_left = JS_ToCString(ctx, op1);
                                const char *trace_right = JS_ToCString(ctx, op2);
                                fprintf(stderr, "LXQJS_OP opcode={label} file=%s fn=%s pc=%td lhs=%s rhs=%s\n",
                                    trace_file, trace_fn ? trace_fn : "", pc - trace_bc->byte_code_buf,
                                    trace_left ? trace_left : "?", trace_right ? trace_right : "?");
                                if (trace_fn) JS_FreeCString(ctx, trace_fn);
                                if (trace_left) JS_FreeCString(ctx, trace_left);
                                if (trace_right) JS_FreeCString(ctx, trace_right);
                            }}
                        }}
                        if (trace_file) JS_FreeCString(ctx, trace_file);
                    }}
                }}"""
    block = block.replace(needle, trace, 1)
    source = source[:start] + block + source[end:]
    changed.append(opcode)

missing = sorted(set(ops) - set(changed))
if missing:
    raise SystemExit("QuickJS opcode trace patch could not locate: " + ", ".join(missing))
path.write_text(source, encoding="utf-8")
print("Instrumented numeric bytecode opcodes:", ", ".join(changed))
