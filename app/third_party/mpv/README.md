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
- The archive contains the upstream full build's `mpv.exe` (the upstream build log reports a statically linked executable) and its accompanying runtime files; CI currently checks the executable and license before any future packaging.

## Licensing and source audit status

mpv's default program license is GPL-2.0-or-later. The upstream full Windows build recipe uses the GPL FFmpeg build container and enables mpv's GPL features. The exact package's license file and build configuration must be preserved and reviewed together; do not infer the complete license of every statically linked component from mpv's own license alone.

The upstream build recipe builds or uses additional components including FFmpeg, LuaJIT, libsixel, libarchive, uchardet, subrandr, cppwinrt/Windows metadata, mujs, and libraries supplied by the FFmpeg build container. FFmpeg is built with GPL and version-3 options in the upstream GPL container. The upstream release build log identifies FFmpeg as `git-2026-11-04-baa68e6`, resolving to commit `baa68e68b34e2de912eb7e94342cebd2545f05ed` (source: https://github.com/FFmpeg/FFmpeg/tree/baa68e68b34e2de912eb7e94342cebd2545f05ed). The build log also records the container image digest as `sha256:146dd9a98dbd12705cc0c2c56d0bb81f83f2f02f1828c83cc08242e64b810fbd`.

The detailed provenance, component inventory, missing source revisions, and release blockers are tracked in [SOURCE_BUNDLE_AUDIT.md](SOURCE_BUNDLE_AUDIT.md). The current upstream full-build recipe uses floating dependency branches and a floating container tag, so the exact complete corresponding-source bundle is not yet established. This candidate remains for internal automated download/hash/launch validation only; it must not be copied into a public My-LiveHub release package until the audit release gate is complete.

## Validation

The `mpv-runtime` job in `.github/workflows/desktop-validation.yml` downloads the pinned asset, verifies SHA-256, extracts it, checks that `mpv.exe` and `LICENSE.GPL` exist, and runs `mpv.exe --no-config --version`. It runs in parallel with the regular client validation and does not publish the runtime as a release artifact.
