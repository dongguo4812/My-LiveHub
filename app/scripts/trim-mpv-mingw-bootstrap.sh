#!/usr/bin/env bash
# Patch mpv v0.42.0's upstream MinGW bootstrap to build only dependencies
# needed by My-LiveHub's current minimal profile.
set -euo pipefail

mpv_src="${1:?Usage: trim-mpv-mingw-bootstrap.sh <mpv-source-dir>}"
bootstrap="$mpv_src/ci/build-mingw64.sh"
[[ -f "$bootstrap" ]] || { echo "Missing upstream bootstrap: $bootstrap" >&2; exit 2; }

python3 - "$bootstrap" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()

replacements = [
    (
        "for x in iconv zlib-ng shaderc spirv-cross amf-headers nv-headers dav1d lcms2; do",
        "for x in iconv zlib-ng dav1d; do",
        "base dependencies",
    ),
    (
        'if [[ "$TARGET" != "i686-"* ]]; then\n    build_if_missing vulkan-headers\n    build_if_missing vulkan-loader\nfi',
        "# Vulkan headers/loader omitted: Vulkan is disabled in My-LiveHub's mpv profile.",
        "Vulkan dependencies",
    ),
    (
        "for x in ffmpeg libplacebo freetype fribidi harfbuzz libass luajit curl; do",
        "for x in ffmpeg libplacebo freetype fribidi harfbuzz libass; do",
        "player dependencies",
    ),
    (
        'if [[ "$TARGET" != "i686-"* ]]; then\n    build_if_missing subrandr\nfi',
        "# subrandr omitted: disabled in My-LiveHub's mpv profile.",
        "subrandr dependency",
    ),
    (
        "-Ddemos=false -D{opengl,d3d11,lcms}=enabled",
        "-Ddemos=false -D{opengl,d3d11}=enabled -Dlcms=disabled",
        "libplacebo optional LCMS dependency",
    ),
]

for old, new, label in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"Expected exactly one {label} block, found {count}; upstream script changed.")
    text = text.replace(old, new, 1)

path.write_text(text)
print("Applied My-LiveHub minimal MinGW dependency profile:")
print("- kept iconv, zlib-ng, dav1d, FFmpeg, libplacebo, font/subtitle stack")
print("- skipped shaderc, SPIRV-Cross, AMF, NV headers, LCMS, Vulkan, LuaJIT, curl, subrandr")
PY
