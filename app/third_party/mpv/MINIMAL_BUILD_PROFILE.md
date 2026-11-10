# My-LiveHub minimal mpv build profile (prototype)

Status: **FIRST-STAGE BUILD SCRIPT ADDED / NOT YET BUILT**

This profile is an isolated build experiment. It does not replace the pinned upstream
v0.42.0 full runtime, the existing Go MpvEngine, or the current CI job.
The full runtime remains the comparison baseline until the prototype passes the
same playback tests and additional video tests.

The first-stage build script is `app/scripts/build-mpv-minimal.sh`. It is intended
to run from the mpv v0.42.0 source tree inside the FFmpeg-Builds Windows x64 GPL
cross-build image. It builds into `build-minimal/` and writes only to
`artifact-minimal/`; it never overwrites My-LiveHub's existing runtime.

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

The options in the build script are based on upstream v0.42.0 meson.options.
The prototype retains the CLI player, WASAPI, D3D11, D3D11VA/DXVA2, and Win32
OpenGL paths, while disabling optional disc, scripting, terminal-video,
non-Windows audio/video, and unused processing backends.

The exact graphics dependencies and output behavior still need a Windows runtime
test. The prototype intentionally does not add libmpv: the current Go engine
controls an independent `mpv.exe` through JSON IPC. If My-LiveHub later requires
video embedded directly into a Wails surface, that rendering integration must be
evaluated separately.

## FFmpeg strategy: staged reduction

**Stage 1 (current script):** pin FFmpeg to the commit observed in the validated
v0.42.0 release build (`baa68e68b34e2de912eb7e94342cebd2545f05ed`) and build it
using the same FFmpeg-Builds container configuration as the upstream recipe.
This isolates the effect of removing mpv-side optional features and dependencies.
It does not yet claim a minimal FFmpeg codec set.

**Stage 2 (after Stage 1 works):** introduce a separately tested FFmpeg configure
profile, retaining only the demuxers, protocols, decoders, parsers and filters
needed by My-LiveHub:

- Demuxers/protocols: local files, HTTP, HTTPS, TCP/TLS, MP3, WAV, Ogg, Matroska,
  MP4/MOV, MPEG-TS, and HLS where required by the selected music/MV providers.
- Audio decoders: MP3, AAC, FLAC, Vorbis, Opus.
- Video decoders: H.264, HEVC, VP8, VP9, AV1, MPEG-4 Part 2 and MPEG-2.
- Parsers and bitstream components required by those formats.
- Filters: `loudnorm`, `alimiter`, `volume`, `aresample`, `equalizer` and
  any additional filters proven necessary by integration tests.
- No encoders, muxers, capture devices, or protocols that are not required for playback.

The list above is a requirements matrix, not a verified FFmpeg configure command.
Stage 2 must validate exact configure switches and inspect the resulting
`ffmpeg -decoders` / `ffmpeg -filters` output.

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
