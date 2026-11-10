# My-LiveHub minimal mpv build profile (prototype)

Status: **DESIGN / NOT BUILT**

This profile is an isolated build experiment. It does not replace the pinned upstream
v0.42.0 full runtime, the existing Go MpvEngine, or the current CI job.
The full runtime remains the comparison baseline until the prototype passes the
same playback tests and additional video tests.

## Product requirements

Keep only capabilities required by My-LiveHub:

- Windows x64, independent executable, JSON IPC, no PATH lookup.
- WASAPI audio output.
- HTTP/HTTPS media input and file input.
- Audio decoders: MP3, AAC, FLAC, Vorbis, Opus.
- Video playback for common online MV formats, with D3D11 video output and
  D3D11VA/DXVA2 hardware decoding where supported; software fallback remains.
- Audio filters needed for loudness normalization and peak limiting.
- No Windows Media Foundation dependency.
- No GUI player controls, DVD/Blu-ray navigation, disc playback, Lua/JavaScript
  scripting, terminal image output, or unrelated platform backends.

## Candidate mpv Meson profile

The following are candidate options from the upstream v0.42.0 meson.options.
They must be validated against the actual cross-build environment before being
treated as final:

- Enable: `cplayer`, `wasapi`, `d3d11`, `d3d-hwaccel`, `d3d9-hwaccel`, `gl-win32`, `libplacebo`, `libass` (only if subtitle support is retained).
- Disable unless a demonstrated runtime requirement appears: `libcurl`, `libarchive`, `libbluray`, `dvda`, `dvdnav`, `cdda`, `dvbin`, `javascript`, `lua`, `rubberband`, `subrandr`, `uchardet`, `vapoursynth`, `sixel`, `shaderc`, `spirv-cross`, `vulkan`, `x11`, `wayland`, `alsa`, `pulse`, `pipewire`, `jack`, `coreaudio`, `cocoa`, `audiounit`.
- Keep the mpv CLI player (`cplayer`) because the current Go engine controls an
  independent `mpv.exe` through JSON IPC. Do not add libmpv to this prototype.

The exact `libplacebo`, `libass`, and graphics options depend on
the selected video output path. If My-LiveHub's MV window uses mpv's own window,
D3D11 output is the first candidate. If it embeds video into a Wails surface, the
rendering integration must be evaluated separately before choosing `libmpv`.

## FFmpeg codec profile (not yet pinned)

Meson feature flags alone will not significantly reduce the executable if the
FFmpeg build still includes the full codec/filter/protocol set. A separate,
minimal FFmpeg configuration is required. It must retain at least:

- Demuxers/protocols: local files, HTTP, HTTPS, TCP/TLS, MP3, WAV, Ogg, Matroska,
  MP4/MOV, MPEG-TS, and HLS where required by the selected music/MV providers.
- Audio decoders: MP3, AAC, FLAC, Vorbis, Opus.
- Video decoders: H.264, HEVC, VP8, VP9, AV1, MPEG-4 Part 2 and MPEG-2.
- Parsers and bitstream components required by those formats.
- Filters: `loudnorm`, `alimiter`, `volume`, `aresample`, `equalizer` and
  any additional filters proven necessary by integration tests.
- No encoders, muxers, capture devices, or protocols that are not required for playback.

This is a requirements list, not a verified FFmpeg configure command.
The exact configure switches and dependency closure must be validated by a clean
build and by inspecting `ffmpeg -decoders` / `ffmpeg -filters`
from the resulting build tools before release.

## Validation gates

1. Build the prototype in a separate output directory; do not overwrite
   `app/runtime/mpv`.
2. Record executable size, total runtime directory size, dependency list, and
   build duration.
3. Run the existing Go JSON IPC playback integration test with null output.
4. Run the same integration test locally with Windows WASAPI and generated audio.
5. Decode representative MP3, AAC/M4A, FLAC, Ogg Vorbis, and Opus fixtures.
6. Test HTTP/HTTPS playback, custom request headers, reconnect/error handling,
   and seek behavior against controlled test endpoints.
7. Test MV playback with H.264/AAC MP4 and at least one HEVC or VP9 sample;
   verify D3D11 hardware decode and software fallback on Windows.
8. Test `loudnorm` / peak limiting against controlled audio samples and
   measure CPU overhead; do not treat a volume slider as loudness normalization.
9. Generate an SBOM and complete corresponding-source archive from the exact
   build. No binary may enter a public release until the existing source/license
   audit release gate is cleared.

## Baseline and decision

The existing upstream v0.42.0 full build (152.25 MiB `mpv.exe` in the
current local validation environment) is the baseline. The prototype is accepted
only if it retains all required media behavior, materially reduces the shipped
runtime footprint, and has a reproducible, auditable build. Otherwise, keep the
known-good full build as an internal test baseline and revise the profile.
