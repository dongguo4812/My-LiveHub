#!/usr/bin/env bash
# Prototype minimal mpv build for My-LiveHub.
#
# Run from the mpv v0.42.0 source tree inside the pinned FFmpeg-Builds
# win64-gpl build container. This script intentionally builds into a separate
# build-minimal directory and never modifies the upstream full build output.
set -euo pipefail

: "${FFBUILD_PREFIX:?Run this script inside the FFmpeg-Builds Windows x64 build image}"
: "${FFBUILD_CROSS_PREFIX:?FFmpeg-Builds cross compiler prefix is missing}"
: "${FF_LIBS:?FFmpeg-Builds linker flags are missing}"

if [[ ! -f meson.build || ! -f ci/build-common.sh ]]; then
    echo "Run this script from the root of the mpv v0.42.0 source tree." >&2
    exit 2
fi

source ./ci/build-common.sh

# Pin the FFmpeg revision observed in the validated v0.42.0 release build.
# This first experiment deliberately keeps the upstream container's FFmpeg
# feature configuration unchanged so the size delta isolates mpv-side trimming.
ffmpeg_commit="baa68e68b34e2de912eb7e94342cebd2545f05ed"
if [[ ! -d ffmpeg/.git ]]; then
    git clone https://github.com/FFmpeg/FFmpeg.git ffmpeg
fi
git -C ffmpeg fetch --depth=1 origin "$ffmpeg_commit"
git -C ffmpeg checkout --detach "$ffmpeg_commit"
(
    cd ffmpeg
    ./configure --prefix="$FFBUILD_PREFIX" --pkg-config-flags="--static" \
        $FFBUILD_TARGET_FLAGS $FF_CONFIGURE \
        --extra-cflags="$FF_CFLAGS" --extra-cxxflags="$FF_CXXFLAGS" \
        --extra-libs="$FF_LIBS" --extra-ldflags="$FF_LDFLAGS" \
        --extra-ldexeflags="$FF_LDEXEFLAGS" \
        --cc="$CC" --cxx="$CXX" --ar="$AR" --ranlib="$RANLIB" --nm="$NM" \
        --enable-static --disable-shared --disable-{doc,programs} \
        --disable-{librav1e,librsvg,openal}
    make -j"$(nproc)"
    make install
)

build_dir="build-minimal"
if [[ -e "$build_dir" ]]; then
    echo "$build_dir already exists; remove it explicitly before rebuilding." >&2
    exit 2
fi

# The prototype keeps the CLI player and only the Windows audio/video paths
# needed by My-LiveHub. FFmpeg remains the codec provider; its exact codec list
# is validated separately by the integration matrix.
meson setup "$build_dir" \
    --cross-file /cross.meson \
    --buildtype=release \
    --default-library=static \
    --prefer-static \
    -Dc_link_args="$FF_LIBS -L$FFBUILD_PREFIX/lib -liconv" \
    -Dcpp_link_args="$FF_LIBS -L$FFBUILD_PREFIX/lib -liconv" \
    -Dgpl=true \
    -Dcplayer=true \
    -Dlibmpv=false \
    -Dtests=false \
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

meson compile -C "$build_dir" mpv.exe mpv.com

mkdir -p artifact-minimal
cp -p "$build_dir"/mpv.exe "$build_dir"/mpv.com LICENSE.GPL artifact-minimal/
"${FFBUILD_CROSS_PREFIX}strip" artifact-minimal/mpv.exe artifact-minimal/mpv.com

echo "Minimal mpv prototype built:"
ls -lh artifact-minimal/
