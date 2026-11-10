#!/usr/bin/env bash
# Build a trimmed mpv CLI runtime using mpv's own tested MinGW dependency
# bootstrap. Run from the root of the checked-out mpv v0.42.0 source tree.
set -euo pipefail

: "${TARGET:?Set TARGET=x86_64-w64-mingw32}"
: "${RUST_TARGET:?Set RUST_TARGET=x86_64-pc-windows-gnu}"

prefix_dir="$PWD/mingw_prefix"
cross_file="$prefix_dir/crossfile"
if [[ ! -f "$cross_file" || ! -f "$prefix_dir/lib/libavcodec.dll.a" ]]; then
    echo "Run ./ci/build-mingw64.sh first to build mpv's MinGW dependencies." >&2
    exit 2
fi

export CC="$TARGET-gcc-posix"
export CXX="$TARGET-g++-posix"
export AR="$TARGET-ar"
export NM="$TARGET-nm"
export RANLIB="$TARGET-ranlib"
export CFLAGS="-O2 -pipe -Wall -I$prefix_dir/include"
export LDFLAGS="-fstack-protector-strong -L$prefix_dir/lib"
source ./ci/build-common.sh

build_dir="build-minimal"
if [[ -e "$build_dir" ]]; then
    echo "$build_dir already exists; remove it explicitly before rebuilding." >&2
    exit 2
fi

# Keep the independent CLI player and Windows audio/video paths. Optional
# player scripting, disc navigation, alternate OS backends and unused filters
# are disabled. FFmpeg and its codecs remain unchanged in this first stage.
meson setup "$build_dir" \
    --cross-file "$cross_file" $common_args \
    --buildtype release \
    -Dlibmpv=false \
    -Dcplayer=true \
    -Dtests=false \
    -Dfuzzers=false \
    -Dbuild-date=false \
    -Dwasapi=enabled \
    -Dd3d11=enabled \
    -Dd3d-hwaccel=enabled \
    -Dd3d9-hwaccel=enabled \
    -Dgl-win32=enabled \
    -Dwin32-threads=enabled \
    -Dwin32-smtc=disabled \
    -Dlibcurl=disabled \
    -Dlibarchive=disabled \
    -Dlibbluray=disabled \
    -Ddvda=disabled \
    -Ddvdnav=disabled \
    -Dcdda=disabled \
    -Ddvbin=disabled \
    -Djavascript=disabled \
    -Dlua=disabled \
    -Drubberband=disabled \
    -Dsdl2-gamepad=disabled \
    -Dsubrandr=disabled \
    -Duchardet=disabled \
    -Dvapoursynth=disabled \
    -Dsixel=disabled \
    -Dshaderc=disabled \
    -Dspirv-cross=disabled \
    -Dvulkan=disabled \
    -Dzimg=disabled \
    -Dlibavdevice=disabled \
    -Djpeg=disabled \
    -Dlcms2=disabled \
    -Damf=disabled \
    -Dalsa=disabled \
    -Dcoreaudio=disabled \
    -Davfoundation=disabled \
    -Daudiounit=disabled \
    -Djack=disabled \
    -Dopenal=disabled \
    -Dpipewire=disabled \
    -Dpulse=disabled \
    -Dsndio=disabled \
    -Doss-audio=disabled \
    -Dsdl2-audio=disabled \
    -Dcocoa=disabled \
    -Dx11=disabled \
    -Dwayland=disabled \
    -Ddrm=disabled \
    -Dgbm=disabled \
    -Dvaapi=disabled \
    -Dvdpau=disabled \
    -Dhtml-build=disabled \
    -Dmanpage-build=disabled \
    -Dpdf-build=disabled

meson compile -C "$build_dir"

artifact_dir="artifact-minimal"
mkdir -p "$artifact_dir"
cp -p "$build_dir/mpv.exe" "$build_dir/mpv.com" "$artifact_dir/"

# This is a staging bundle, not the final trimmed dependency set. Include
# runtime DLLs so the prototype is runnable; later dependency analysis will
# prune unused DLLs and produce the final size comparison.
cp -p "$prefix_dir"/bin/*.dll "$artifact_dir/" 2>/dev/null || true
for file in /usr/lib/gcc/"$TARGET"/*-posix/*.dll /usr/"$TARGET"/lib/*.dll; do
    [[ -f "$file" ]] && cp -p "$file" "$artifact_dir/"
done
cp -p LICENSE.GPL "$artifact_dir/" 2>/dev/null || true

{
    echo "mpv source: v0.42.0"
    echo "target: $TARGET"
    echo "FFmpeg commit: $(git -C ffmpeg rev-parse HEAD)"
    echo "Dependency configuration: upstream ci/build-mingw64.sh"
    echo "NOTE: DLL directory is intentionally unpruned staging output."
    echo
    echo "Executable sizes:"
    ls -lh "$artifact_dir/mpv.exe" "$artifact_dir/mpv.com"
    echo
    echo "DLL count and size:"
    find "$artifact_dir" -maxdepth 1 -type f -name '*.dll' | wc -l
    du -sh "$artifact_dir"
    echo
    echo "Direct mpv.exe imports:"
    "$TARGET-objdump" -p "$artifact_dir/mpv.exe" | sed -n '/The Import Tables/,/The Export Tables/p' | grep 'DLL Name' || true
} | tee "$artifact_dir/BUILD-REPORT.txt"

echo "Prototype staging bundle: $artifact_dir"
