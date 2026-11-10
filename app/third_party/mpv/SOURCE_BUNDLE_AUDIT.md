# mpv v0.42.0 Windows full build — source and license audit

Status: **NOT CLEARED FOR REDISTRIBUTION**

This is an engineering provenance record, not legal advice and not a claim that the current binary has a complete corresponding-source package.

## Binary under evaluation

| Field | Value |
|---|---|
| Upstream | https://github.com/mpv-player/mpv |
| Tag | `v0.42.0` |
| Asset | `mpv-v0.42.0-x86_64-w64-mingw32-full.zip` |
| SHA-256 | `c7d4ff6a67dd33659f71d81efa56335fea2213c7522fd67235f3abf0d9ea1dbd` |
| Target | Windows x86_64, MinGW-w64 |
| Build profile | Full, static, GPL-enabled |
| Included mpv notice observed | `LICENSE.GPL` |

The mpv Windows compilation documentation states that the full release builds are statically linked and that the build uses the FFmpeg-Builds container and its static dependency libraries:
https://github.com/mpv-player/mpv/blob/v0.42.0/DOCS/compile-windows.md

## Build configuration and observed components

The tagged upstream build recipe is:
https://github.com/mpv-player/mpv/blob/v0.42.0/ci/build-mingw64-full.sh

It builds/uses the following directly or through the FFmpeg-Builds container:

| Component | Provenance currently recorded | License/source status |
|---|---|---|
| mpv | tag `v0.42.0` | Source tag identified; preserve `Copyright`, `LICENSE.GPL`, all file-level notices |
| FFmpeg | commit `baa68e68b34e2de912eb7e94342cebd2545f05ed` | Exact source commit identified; GPL/version-3 configuration is used; collect full source and COPYING files |
| LuaJIT | upstream recipe clones branch `v2.1`; build log reports `2.1.1793383622` | Exact commit still required |
| libsixel | upstream recipe clones default branch | Exact commit and license text still required |
| libarchive | upstream recipe clones default branch; build log reports `3.9.0` | Exact commit and license text still required |
| uchardet | tag `v0.0.8` | Source tag identified; collect license and source |
| subrandr | upstream recipe builds default branch | Exact commit and transitive Rust crate lock/source still required |
| cppwinrt | release `2.0.250303.1` | Source and license to archive |
| windows-rs metadata | branch `73` | Exact commit and applicable notices still required |
| mujs | Meson WrapDB `1.3.8-1` | Source and patch revision to archive |
| libplacebo | build log reports `7.387.0` | Exact source commit and notices still required |
| libass | build log reports `0.17.5` | Exact source commit and notices still required |
| libbluray | enabled in build; exact version/commit not yet recorded | Pending |
| libcurl | enabled in build; exact version/commit not yet recorded | Pending |
| shaderc / SPIRV-Tools / glslang | enabled in build; exact revisions not yet recorded | Pending |
| SPIRV-Cross | enabled in build; exact revision not yet recorded | Pending |
| zimg | enabled in build; exact version/commit not yet recorded | Pending |
| rubberband | GPL profile enables it; exact version/commit not yet recorded | Pending |
| FFmpeg-Builds image dependencies | image digest recorded as `sha256:146dd9a98dbd12705cc0c2c56d0bb81f83f2f02f1828c83cc08242e64b810fbd` | Full package bill of materials, source revisions and notices not yet available |

The release build log also reported these versions during configuration: libplacebo `7.387.0`, libass `0.17.5`, libarchive `3.9.0`, LuaJIT `2.1.1793383622`, uchardet `0.0.8`, libsixel `1.8.7-r2`, and mujs `1.3.8`. A version string alone is not a substitute for the exact source revision.

## Why redistribution is still blocked

The upstream recipe uses floating branches/default branches (including the FFmpeg-Builds `latest` image and several dependency branches). The build log and container image digest help identify the build, but do not yet establish a complete, machine-readable source bill of materials for every statically linked library and transitive dependency.

A copy of mpv's `LICENSE.GPL` alone is not sufficient for the complete full build. The exact corresponding source must cover mpv, linked libraries and the scripts/configuration needed to rebuild the distributed executable, with the relevant license texts and notices. FFmpeg's GPL/version-3 build options also mean we must preserve and review the applicable GPLv3 materials.

## Required release gate

Do not copy this candidate into a public My-LiveHub release package until all boxes are checked:

- [ ] Obtain a complete build-time SBOM from the exact binary build, including static FFmpeg-Builds image contents.
- [ ] Pin exact source commits/archives for every linked component and transitive dependency.
- [ ] Archive the complete corresponding source, including patches, wrap files, Cargo lockfiles/crate sources, and build/install scripts.
- [ ] Include mpv GPL text, FFmpeg GPLv3 text, and every third-party license/notice required by the actual components.
- [ ] Verify the source bundle can be downloaded without authentication and rebuilds the same configured binary.
- [ ] Publish the source bundle alongside the binary, or implement and legally review a compliant written source offer.
- [ ] Have the final redistribution arrangement reviewed for the intended commercial/non-commercial distribution model.

Until then, CI is allowed to download, hash-check and launch-test this binary for internal compatibility validation only. It must not upload it as a public My-LiveHub release asset.


## Source-level findings from the tagged build recipe

A direct review of `ci/build-mingw64-full.sh` at mpv tag `v0.42.0` confirms that the published full binary cannot be treated as reproducible from the mpv tag alone:

- The recipe uses `git clone --depth=1 --recursive --shallow-submodules` for several dependencies without a commit ID or immutable tag. In particular, LuaJIT is cloned from branch `v2.1`; libsixel and libarchive are cloned from their default branches.
- FFmpeg is cloned from the default branch with the same shallow-clone helper. The recorded FFmpeg commit above came from the observed build log, not from a pin in the recipe.
- uchardet is pinned to tag `v0.0.8`, but this does not pin the other dependencies.
- cppwinrt is downloaded by version `2.0.250303.1`; windows-rs is cloned from branch `73`, not a commit.
- mpv dependencies are populated using `meson wrap install mujs` and `meson subprojects download`. The resulting wrap/subproject revisions and patches must be captured from the actual build workspace; the mpv tag alone does not identify them.
- The container reference in the recipe is `ghcr.io/btbn/ffmpeg-builds/win64-gpl:latest`. A digest observed for one build is useful provenance, but the digest by itself does not provide the complete source and build recipe for every library in the image.
- The script enables a broad set of features, including libarchive, libbluray, libcurl, shaderc, SPIRV-Cross, subrandr, uchardet, zimg and Vulkan; the GPL profile additionally enables DVD navigation and rubberband. This reinforces that the corresponding-source inventory must be based on the actual linked build, not only on the list of mpv direct dependencies.

The upstream FFmpeg-Builds repository documents that its `gpl` variant includes GPL-only dependencies and that package build recipes live under `scripts.d`. Its image/build pipeline is a separate source tree that must also be pinned and archived when relying on its prebuilt static libraries:
https://github.com/BtbN/FFmpeg-Builds

**Audit conclusion:** the recipe is valuable provenance and a starting point for an independently controlled rebuild, but it does not establish the exact source closure for the already-published mpv ZIP. Do not infer missing commits from version strings or current upstream branches.
