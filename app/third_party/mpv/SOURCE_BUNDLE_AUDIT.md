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


## Additional audit: FFmpeg-Builds image does not close the source gap

The FFmpeg-Builds source tree was reviewed separately from the mpv recipe. This confirms that the observed container digest is not, by itself, a corresponding-source bundle:

- The FFmpeg-Builds README describes its GPL variant as including GPL-only dependencies, lists scripts.d as the per-package build recipe directory, and describes its latest image/release references as floating. See https://github.com/FFmpeg-Builds/FFmpeg-Builds
- In the current FFmpeg-Builds build.sh, the FFmpeg source branch defaults to master (unless overridden), and the build container clones that branch at build time. The script then packages the installed prefix and copies the FFmpeg license file; it does not itself archive the source tree and all dependency sources into the output package. See https://github.com/FFmpeg-Builds/FFmpeg-Builds/blob/master/build.sh
- makeimage.sh runs download.sh, generates a Dockerfile from the selected package scripts, and builds the image through Docker Buildx. See https://github.com/FFmpeg-Builds/FFmpeg-Builds/blob/master/makeimage.sh
- generate.sh sources package scripts from scripts.d, resolves their declared dependencies, and emits Docker build stages. Consequently, the actual dependency closure depends on the exact FFmpeg-Builds repository revision, selected variant/addins, generated Dockerfile, downloaded source archives, and the base/target image contents. See https://github.com/FFmpeg-Builds/FFmpeg-Builds/blob/master/generate.sh

This review does not prove that FFmpeg-Builds lacks all possible provenance records or that the exact image can never be traced. It establishes the narrower point that the public build scripts and an image digest alone do not give My-LiveHub the exact source closure for the image used by the observed mpv build. No complete SBOM, generated Dockerfile, package-source manifest, or full build log tied to the observed image digest has yet been collected.

### What would be required to reuse this image in a compliant rebuild

For the exact FFmpeg-Builds image digest used by mpv, obtain and preserve:

1. The FFmpeg-Builds Git commit used to generate the image.
2. The exact TARGET, VARIANT, and ADDINS values, plus the generated Dockerfile and all build scripts.
3. Every downloaded source URL, immutable revision/archive hash, patch, and build option used by the resolved scripts.d dependency graph.
4. The base and target image digests, their Dockerfiles, toolchain/compiler versions, and package inventory.
5. The exact FFmpeg source commit and configure flags used by the mpv build.
6. License texts and corresponding source for all statically linked libraries, including transitive dependencies.

If these records cannot be recovered from the historical build, the defensible route is to build a new pinned toolchain and dependency image under My-LiveHub-controlled CI, then generate the SBOM and corresponding-source archive during that build. Do not represent a new rebuild as the source for the already-published upstream mpv ZIP.


## Follow-up refinement: distinguish FFmpeg source from dependency recipes

A further review of the current FFmpeg-Builds `build.sh` narrows the earlier finding:

- The script sets `GIT_BRANCH` to `master` by default (unless overridden) and performs `git clone --branch='$GIT_BRANCH'` for FFmpeg inside the image. This is a floating FFmpeg source selection, not an immutable commit pin.
- The script packages the installed FFmpeg prefix and copies the selected FFmpeg license file into the output. It does not package the FFmpeg source checkout or the complete dependency source tree.
- The repository README points to `scripts.d/` as the per-package build recipe inventory. Those package scripts must be audited individually at the exact FFmpeg-Builds commit used for the image. It would be inaccurate to infer from the FFmpeg clone behavior alone that every third-party package is also floating; equally, it would be unsafe to assume their revisions are pinned without inspecting the exact scripts and generated Dockerfile.
- The current GitHub Actions workflow includes a `download-cache` artifact flow for cached dependency downloads. This is a possible source of additional historical evidence, but no such artifact has yet been tied to the exact image digest observed in the mpv build. It therefore does not currently close the provenance gap.

References reviewed:
- https://github.com/BtbN/FFmpeg-Builds/blob/master/build.sh
- https://github.com/BtbN/FFmpeg-Builds/blob/master/README.md
- https://github.com/BtbN/FFmpeg-Builds/blob/master/.github/workflows/build.yml

The remaining blocking question is not whether FFmpeg-Builds has package recipes; it is whether we can associate one exact repository revision, generated build definition, downloaded source set, base/toolchain image set, and build configuration with the specific historical image digest consumed by mpv v0.42.0. Until that association is proven, the historical ZIP's full corresponding-source closure remains unverified.

## Alternative prebuilt distributions and corresponding-source approaches reviewed

The following alternatives were checked before deciding whether to keep using the v0.42.0 official full ZIP.

### 1. shinchiro/mpv-winbuild-cmake

Repository: https://github.com/shinchiro/mpv-winbuild-cmake

This project publishes ready-to-run Windows mpv.exe archives, so it is technically a suitable independent-process player. However, its current build recipes do not pin all dependencies to immutable revisions: the mpv and FFmpeg ExternalProject definitions clone their repositories without a GIT_TAG, and the build workflow runs ninja update. The workflow also retains detailed build logs for only one day.

Public redistribution requests confirm that this is a real provenance gap, not merely a theoretical concern:

- Issue #848 asks for exact dependency revisions and corresponding sources for the 20260903 build. A later comment points to a different LGPL libmpv project, not a matching mpv.exe source bundle.
- Issue #856 asks for the full dependency manifest for the 20260809 libmpv build; it records that floating branches and expired detailed logs prevent identifying every effective revision.
- Issue #858 asks the same for the 20261021 mpv.exe build; its author reports that the detailed log artifact expired and one dependency fetch failed, leaving an exact dependency revision unknown.

These issues remain open at the time of this review. Do not treat the mpv commit hash in the filename, the top-level recipe archive, or the FFmpeg version as the complete source closure.

References:
- https://github.com/shinchiro/mpv-winbuild-cmake/blob/master/packages/mpv.cmake
- https://github.com/shinchiro/mpv-winbuild-cmake/blob/master/packages/ffmpeg.cmake
- https://github.com/shinchiro/mpv-winbuild-cmake/blob/master/.github/workflows/mpv_gcc.yml
- https://github.com/shinchiro/mpv-winbuild-cmake/issues/848
- https://github.com/shinchiro/mpv-winbuild-cmake/issues/856
- https://github.com/shinchiro/mpv-winbuild-cmake/issues/858

### 2. Reelm / MediaBoxMc corresponding-source publication

Page: https://mediaboxmc.com/oss/mpv/20260610-304426c/

This is a useful example of redistributing an unmodified, standalone mpv process: the published binary is an mpv.exe archive from shinchiro's 20260610 release, and the publisher provides a machine-readable BUILD_MANIFEST, hashes, and source archives for the build recipe, mpv, FFmpeg, and uosc.

Important limitation: the manifest does not mirror every dependency source archive. It instead publishes a written source offer for the remaining GPL/LGPL components. That offer belongs to the publisher and cannot automatically be treated as My-LiveHub's own offer. We would need to obtain and verify the remaining corresponding sources ourselves, or obtain an explicit arrangement that actually covers My-LiveHub's recipients and distribution period.

This is a possible model, not yet a cleared binary for My-LiveHub.

References:
- https://mediaboxmc.com/oss/mpv/20260610-304426c/
- https://mediaboxmc.com/oss/mpv/20260610-304426c/BUILD_MANIFEST.json

### 3. freeKC/libmpv-builds — strongest source-management example, but a different runtime shape

Repository: https://github.com/freeKC/libmpv-builds

This project demonstrates a much cleaner provenance workflow: immutable commits for Git dependencies, SHA-256 pins for source archives and recipe files, a BUILDINFO file, a complete source archive, per-component license notices, and checksums published alongside each binary.

However, its Windows output is libmpv-2.dll (a library loaded into an application), not a standalone mpv.exe process. It builds with -Dgpl=false and an LGPL-oriented FFmpeg configuration. It cannot be substituted into the already-approved independent mpv process design without changing the playback architecture and Go integration.

The useful takeaway is to reuse its source-locking, source-archive, BUILDINFO and license-collection approach for a future standalone mpv.exe build, not to silently replace the selected runtime with libmpv.

References:
- https://github.com/freeKC/libmpv-builds
- https://github.com/freeKC/libmpv-builds/blob/main/windows/sources.lock
- https://github.com/freeKC/libmpv-builds/blob/main/README.md

### 4. Why Bilibili-MagicalDanmaku does not need mpv

The inspected trytrytogo/Bilibili-MagicalDanmaku source does not integrate mpv. Its Qt project links Qt Multimedia and its order-song player instantiates QMediaPlayer; playback is started from a local file URL. The project file also lists a LAV Filters installer among its distribution files. This is a different playback stack, and it does not establish that an mpv binary or its source can be redistributed without the corresponding-source obligations.

References:
- https://github.com/trytrytogo/Bilibili-MagicalDanmaku/blob/master/Bilibili-MagicalDanmaku.pro
- https://github.com/trytrytogo/Bilibili-MagicalDanmaku/blob/master/order_player/orderplayerwindow.cpp

## Current decision

No ready-to-use Windows x64 standalone mpv.exe candidate reviewed so far has both (a) the required runtime shape and (b) a publicly mirrored, fully verified corresponding-source closure that My-LiveHub can safely reuse as-is.

The lowest-risk next step is not to rebuild the entire mpv stack immediately. First request/locate a complete source bundle for a specific existing standalone mpv.exe build, including the actual dependency revisions and notices. In parallel, retain freeKC's source-locking workflow as a proven technical template if the existing prebuilt binary cannot be cleared.

Do not switch to libmpv, Qt Multimedia, LAV Filters, or Windows Media Foundation without an explicit architecture decision. The selected My-LiveHub direction remains a separately packaged mpv.exe controlled through JSON IPC.


## 20260610 Reelm / MediaBoxMc candidate — stronger corresponding-source evidence

Review date: 2026-11-10.

A substantially better prebuilt standalone candidate was found:

- Binary: `mpv-x86_64-20260610-git-304426c.7z`
- Upstream origin stated by publisher: `shinchiro/mpv-winbuild-cmake`, build batch `20260610`
- SHA-256: `facac536baa73c7b925771af5e39a3c9cb16b8d75b59a6e9800de89799dffca7`
- mpv commit: `304426c390901436fb1d4a63efbd582ae80c88f4`
- FFmpeg commit: `2576e0943`
- Build recipe archive SHA-256: `1697829974ed8fdda0f8e3b0c69c424d7a00f2f6534b5389ff17bf2cc3813f38`
- mpv source archive SHA-256: `3a3802868d7883bc8496184b6a0c6ee91e49ce4ec9e4bb3d0b854663371fd740`
- FFmpeg source archive SHA-256: `0b8ded4892189fbfb4fe0e0c74d399fc7ed6c0fb7933337736905a2ea567b31a`
- uosc source archive SHA-256: `a84476d6826406f1eb0815c2ce0c1318858f18de8d77c47b4387175641a13ba9`
- Published build workflow run: `https://github.com/shinchiro/mpv-winbuild-cmake/actions/runs/27243718577`, completed successfully on 2026-06-10.

Publisher page: https://mediaboxmc.com/oss/mpv/20260610-304426c/
Machine-readable manifest: https://mediaboxmc.com/oss/mpv/20260610-304426c/BUILD_MANIFEST.json

The publisher explicitly describes this as complete corresponding source for its redistributed binary, and says the standalone mpv process is separate from its application. Its manifest says the build recipe pins every component and cross-compiles for Windows. The page mirrors the build recipe, mpv, FFmpeg and uosc source archives, and publishes a written offer for the complete corresponding source of remaining GPL/LGPL components for at least three years, at no charge beyond distribution cost.

This is materially stronger than the earlier v0.42.0 official full ZIP candidate. It may allow My-LiveHub to reuse an already compiled standalone mpv.exe without rebuilding the media stack.

### Remaining checks before My-LiveHub redistribution

This candidate is NOT YET CLEARED for My-LiveHub. The publisher's source offer is issued by MediaBoxMc / Reelm for its own distribution. My-LiveHub must not assume that the offer automatically satisfies My-LiveHub's own obligations or recipients.

Before adopting it:
1. Obtain and retain the exact binary archive and verify the SHA-256 above.
2. Download and hash-verify every published source archive and inspect the build recipe archive to confirm its actual immutable dependency pins and patch/wrap inputs.
3. Request from the publisher the full corresponding source for every remaining GPL/LGPL component covered by its written offer, including source archives and all associated patches/build scripts/notices; alternatively obtain a written arrangement explicitly allowing My-LiveHub to rely on the offer for My-LiveHub's own recipients and distribution period.
4. Verify the archive's included runtime files and license notices, including the uosc script/config asset and any other bundled files, against the manifest.
5. Verify that the binary itself matches the published SHA-256 and starts on Windows x64. Then verify actual audio playback/IPC behavior in My-LiveHub before changing the pinned runtime.
6. Have the final distribution method and notices reviewed for the applicable GPL/LGPL obligations.

The web reader exposed the manifest and archive links, but the source archive bytes have not yet been downloaded and independently hash-verified in this review. The manifest is therefore publisher-provided evidence, not an independent reproduction result.

### Candidate decision

Keep the existing v0.42.0 ZIP as an internal validation artifact only. Prioritize the Reelm 20260610 standalone binary as the next candidate because it has a concrete binary hash, exact mpv/FFmpeg revisions, a pinned build-recipe archive, a successful build workflow reference, source archive hashes, and a published written offer for the remaining GPL/LGPL components.

Do not switch runtime pins or publish this candidate in a My-LiveHub release until the checks above are completed.


### Follow-up verification on 2026-11-10

The publisher's machine-readable manifest was opened and its full fields inspected. It explicitly lists only three directly mirrored source components (mpv, FFmpeg and uosc) plus the build-recipe archive. It does NOT enumerate every dependency as an individual source archive; instead, it states that all remaining GPL/LGPL components are available under its written offer. The page gives `contact@rvsg.net` as the contact address.

The manifest asserts that the 20260610 CMake superbuild pins every component, including x264, x265, libass, fribidi and harfbuzz. This is a publisher assertion that still needs independent inspection of the actual recipe archive. The recipe archive download was attempted during this review but the retrieval client rejected the gzip content type; no archive bytes or SHA-256 were independently verified here.

The upstream release URL named by the manifest (`https://github.com/shinchiro/mpv-winbuild-cmake/releases/tag/20260610`) returned a 404 through the current web fetch attempt, while the corresponding-source page remains accessible and identifies the asset hash. This is not proof that the release asset is deleted; it means binary availability must be rechecked through another route before adopting it.

Consequently, status remains `CANDIDATE — NOT CLEARED`. The immediate blocker is obtaining the complete remaining source set under the written offer and verifying the actual binary/archive availability and hashes. No runtime pin has been changed.


### CI and upstream workflow verification — 2026-11-10

Additional evidence obtained after the previous review:

- The upstream GitHub Actions run `27243718577` exists, completed with conclusion `success`, and its jobs include successful x86_64 build, packaging, and release-upload steps. The run's checked-out workflow commit is `5efd298cb51513c2410e4e9029b5e56b83c2aaac` (2026-05-16), not a commit named/tagged `20260610`. This is evidence that the build workflow succeeded, but does not by itself prove the exact source closure claimed by the manifest.
- My-LiveHub's existing Windows x64 validation job in run `42824022692` successfully downloaded the pinned candidate archive, verified its SHA-256, and started mpv. This independently confirms the binary is obtainable through the CI download route, its published hash matches, and the executable starts in the CI Windows environment. It does NOT yet validate real media playback, IPC, or source completeness.
- The same My-LiveHub workflow's general frontend/Go validation job was cancelled during dependency installation; frontend build, Go tests and Windows executable build were skipped. Therefore that workflow run is not a full CI pass.
- GitHub's release-by-tag API lookup for `20260610` did not return a release object, despite the successful workflow run and successful My-LiveHub binary download. Do not infer that the binary is unavailable: the CI download route succeeded. The publisher's precise provenance path still needs clarification.

### License metadata discrepancy

The publisher's own pages are inconsistent: its corresponding-source page identifies the binary as `GPL-2.0-or-later` (with FFmpeg statically linked), and its third-party notices also identify MPV as GPL-2.0-or-later; however, its app terms page describes MPV as LGPL-2.1-or-later. These are materially different license statements. Treat the LGPL wording in the app terms as an apparent documentation inconsistency, not as permission to treat this binary as LGPL. For My-LiveHub's assessment, use the actual licenses of the exact shipped components and request written clarification from the publisher. The separate-process architecture is useful technical separation, but does not remove mpv's own source and notice obligations.

References:
- https://github.com/shinchiro/mpv-winbuild-cmake/actions/runs/27243718577
- https://github.com/dongguo4812/My-LiveHub/actions/runs/42824022692
- https://mediaboxmc.com/app/terms/
- https://mediaboxmc.com/nl/legal/third-party-notices/

Updated status: the binary itself has passed hash and startup validation in My-LiveHub CI, so the earlier binary availability/hash/startup blockers are cleared. The candidate remains `NOT CLEARED FOR REDISTRIBUTION` until complete corresponding source materials, archive contents, and license metadata are independently reconciled. Real playback/IPC validation remains outstanding.
