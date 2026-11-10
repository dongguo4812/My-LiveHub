# mpv Windows runtime candidate

This directory records the pinned upstream runtime candidate used for compatibility validation. It is not yet the final redistribution/source-compliance bundle.

## Selected candidate

- Upstream project: https://github.com/mpv-player/mpv
- Release: `v0.42.0`
- Asset: `mpv-v0.42.0-x86_64-w64-mingw32-full.zip`
- Download: https://github.com/mpv-player/mpv/releases/download/v0.42.0/mpv-v0.42.0-x86_64-w64-mingw32-full.zip
- SHA-256: `c7d4ff6a67dd33659f71d81efa56335fea2213c7522fd67235f3abf0d9ea1dbd`
- Architecture: Windows x86_64
- Build type: upstream full MinGW-w64 build; upstream describes full builds as static builds intended to provide the complete experience.
- Upstream release source: https://github.com/mpv-player/mpv/tree/v0.42.0
- Upstream build recipe: https://github.com/mpv-player/mpv/blob/v0.42.0/ci/build-mingw64-full.sh
- The archive's packaged license file is `LICENSE.GPL`.

## Licensing and source audit status

mpv's default program license is GPL-2.0-or-later. The upstream full Windows build recipe uses the GPL FFmpeg build container and enables mpv's GPL features. The exact package's license file and build configuration must be preserved and reviewed together; do not infer the complete license of every statically linked component from mpv's own license alone.

The upstream build recipe builds or uses additional components including FFmpeg, LuaJIT, libsixel, libarchive, uchardet, subrandr, cppwinrt/Windows metadata, mujs, and libraries supplied by the FFmpeg build container. FFmpeg is built with GPL and version-3 options in the upstream GPL container.

The exact corresponding-source bundle and complete third-party component/license inventory are still being assembled. Therefore this candidate is currently used for automated download/hash/launch validation only; it must not be treated as a completed public redistribution package until the source offer and all applicable notices are complete.

## Validation

The `mpv-runtime` job in `.github/workflows/desktop-validation.yml` downloads the pinned asset, verifies SHA-256, extracts it, checks that `mpv.exe` and `LICENSE.GPL` exist, and runs `mpv.exe --no-config --version`. It runs in parallel with the regular client validation and does not publish the runtime as a release artifact.
