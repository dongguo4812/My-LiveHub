# My-LiveHub 独立音乐播放器改造方案（Go + QuickJS-NG + HTMLAudio）

> 状态：开发基线方案  
> 目标分支：`feat/lx-js-runtime-quickjs-poc`  
> 最终架构：Go + QuickJS-NG + HTMLAudio + Vue/Wails WebView  
> 核心架构约束：继续使用洛雪 JS 音源脚本；通过 QuickJS-NG 模拟洛雪 JS 运行时环境，兼容导入脚本所需的宿主 API/全局对象/请求行为，实现搜索、详情、播放 URL、歌词等能力；HTMLAudio 只负责最终音频播放。复用现有音乐业务层，不引入独立播放器进程。

> **当前执行快照（2026-11-23，按分支代码与现有审计证据更新）**
> - 当前分支：`feat/lx-js-runtime-quickjs-poc`；本方案仍是唯一开发依据。
> - A 阶段生产脚本导入、持久化、启停/删除、损坏脚本隔离及 QuickJS production runtime 生命周期已完成并通过 CI Run #544。
> - B 阶段统一 action 调度与歌曲模型接线已完成并通过 CI Run #567：生产搜索/详情/歌词/封面扩展 action 已接线；按脚本 inited.sources 做音源范围路由；原生 musicUrl 仅按原生声明执行，不与社区扩展 action 混用；Track 映射、错误分类、多脚本隔离及生产 fixture 已通过测试。
> - C 阶段 Go 侧播放资源解析、音质回退、TTL 缓存、资源身份校验、资源感知端口和幂等媒体失败刷新已完成（CI Run 45142573201）。D 已接通生产 HTMLAudio PlaybackEngine、Wails 命令/事件桥、应用级单例 Audio、播放控制、错误回传、一次 URL 失效刷新重试和切歌取消；CI Run 45149682218 的 Go/desktop、bindings、frontend 全部通过。Windows 可执行文件已由 CI Run 45150651612 构建并作为 artifact 发布；目标 WebView 真实来源可听播放仍待设备实测。
> - A/B/C 均已达到第 4 节各自完成门；当前唯一执行阶段为 D。D 的代码、自动化 CI 和 Windows PoC 构建子门已通过，目标设备实播验收未完成前不得进入 E。测试证据只覆盖明确列出的契约，不扩大为“完全兼容洛雪”。

## 1. 项目目标与最终边界

My-LiveHub 自己完成音乐搜索、音源脚本运行、播放 URL 解析、播放队列管理、音频播放、歌词同步和播放状态管理。运行音乐模块不需要安装、启动或连接任何第三方桌面播放器。

### 1.1 技术职责

| 层 | 技术 | 职责 |
|---|---|---|
| UI | Vue | 播放器界面、搜索结果、队列、歌词、播放控制交互 |
| 音频输出 | WebView HTMLAudio | 实际音频加载、解码、播放、暂停、跳转、音量和播放事件 |
| 业务服务 | Go | 统一歌曲模型、音源调度、队列、历史、播放会话、缓存、错误恢复 |
| 音源运行时 | QuickJS-NG + 洛雪运行时兼容层 | 导入并运行洛雪 JS 音源脚本；模拟洛雪宿主环境及 API，提供搜索、详情、播放 URL、歌词等脚本能力 |
| HTTP/安全边界 | Go HTTP 客户端与受控 Host API | 音源网络请求、超时、取消、请求头策略和资源校验 |

### 1.2 洛雪 JS 兼容原则（强制约束）

- **继续以洛雪 JS 音源脚本作为音源扩展和平台适配的核心，不改为逐个平台重写 API。** 用户可导入符合洛雪脚本规范的 JS 音源；内置脚本与用户导入脚本走同一运行时入口。
- QuickJS-NG 不是简单执行器：必须实现/模拟洛雪 JS 运行时环境，包括脚本加载与元信息、脚本生命周期、模块/全局对象、`lx` 宿主对象及其方法、请求/响应对象、事件/回调、定时与存储等脚本实际依赖的能力。
- 兼容性以洛雪 JS API/行为为基准，通过官方/真实脚本样本建立能力矩阵；不得只针对当前三个内置脚本硬编码，也不得把“能执行 `musicUrl`”等同于“洛雪运行时兼容完成”。
- 搜索、歌曲详情、播放 URL、歌词等能力应由导入脚本提供的对应 action/接口执行。Go 负责调度、统一模型和权限边界，QuickJS 负责脚本执行；不得绕过脚本而为五个平台分别另写一套业务 API。
- 脚本运行时与最终媒体播放分层：脚本负责解析 URL 及其必要媒体请求上下文；Go 校验并封装播放资源；HTMLAudio（必要时受控 Go 流代理）负责实际媒体传输/播放。
- 脚本发起的 API 请求上下文与最终媒体 URL 的 GET 请求上下文必须分别建模。脚本 `lx.request` 的 headers 不会自动成为 HTMLAudio 请求头；解析结果需要能够表达媒体请求所需的 headers、Cookie/Referer、有效期及刷新方式，并经过安全过滤。

### 1.3 明确不采用

- 不启动洛雪音乐客户端，不调用 `lxmusic://` 播放协议。
- 不依赖洛雪本地 API（例如默认的 `127.0.0.1:23330`）完成播放。
- 不启动 MusicFree 或其他外部播放器进程。
- 不采用 mpv。
- 不引入 malgo、miniaudio 或其他原生独立音频引擎。
- 不新增 Electron、Node.js 或 Python 播放器运行时。
- 不重写现有 Go 搜索、音源注册、队列和歌曲模型来迁就新播放器。

## 2. 当前代码基线（开始改造前已核对）

当前分支已有以下实现：

- `app/internal/music/engine.go` 定义了 Go `PlaybackEngine`，当前接口包含 `Load/Pause/Resume/Stop/Snapshot`。
- `app/internal/desktop/music_service.go` 已有 `MusicService`，管理音源注册、队列、播放状态、歌词缓存及脚本运行时；默认构造函数仍注入 `NewLXPlaybackEngine(value.LXAPIEndpoints)`。
- `app/internal/desktop/lx_playback_engine.go` 通过洛雪本地 API、`lxmusic://music/play` 和系统协议启动外部客户端，是必须被替换的旧播放实现。
- `app/internal/config/music.go` 仍包含 `LXAPIEndpoints` 配置和默认地址，迁移时必须兼容旧配置文件。
- `app/frontend/src/views/MusicView.vue` 已有音乐页面、搜索、队列和播放交互，可在此基础上接入统一 HTMLAudio 播放器，不另造整套页面。
- `app/internal/music/lifecycle.go` 定义了播放引擎生命周期接口。

以上是代码结构事实。当前阶段状态：**D 的生产 HTMLAudio 桥接与自动化测试已实现；Windows PoC 可执行文件已构建；真实目标 WebView 可听播放尚未验证。**

## 3. 目标架构与播放数据流

### 3.1 播放流程

1. Vue 将用户选择的歌曲提交给 Go MusicService。
2. Go 依据统一歌曲模型和音源信息调用对应音源适配器/QuickJS-NG 脚本。
3. Go 获得播放资源：URL、可用音质、必要请求头/鉴权信息、过期信息及来源元数据。
4. Go 校验播放资源，并建立播放会话，将资源交给前端 HTMLAudio 播放适配层。
5. Vue 中维护唯一、长期复用的 HTMLAudio 实例，设置资源并调用浏览器播放 API。
6. HTMLAudio 的 `playing`、`pause`、`timeupdate`、`durationchange`、`ended`、`error` 等事件回传 Go/前端状态层。
7. Go 继续负责队列推进、自动切歌、播放历史和业务事件；Vue 负责展示与音频元素交互。
8. 停止、切歌或销毁模块时，取消旧请求、移除监听、清理旧资源引用，避免旧会话事件覆盖新会话。

### 3.2 组件边界

- **Go MusicService** 是业务状态和队列的权威来源，不把队列复制到播放器内部。
- **HTMLAudio Adapter** 是音频执行器，不承载音源搜索、签名算法或业务队列。
- **QuickJS-NG** 只运行音源脚本，不直接访问 DOM、Audio、文件系统或进程。
- **Vue** 维护单例 Audio 实例和必要的 UI 状态，不创建每首歌一个 Audio 实例。
- 播放会话必须具有递增的 generation/session ID；过期会话的异步结果和事件必须被丢弃。

### 3.3 播放资源契约（实施时固化）

统一播放资源至少需要表达：

- 可播放 URL（不得在日志中输出完整签名 URL 或敏感查询参数）。
- 音源 ID、歌曲 ID、音质标识。
- MIME/容器/编码信息（若音源提供）。
- 过期时间或可重新解析标记（若可获得）。
- 请求头能力需求和来源元数据。
- 是否允许代理、是否需要 Cookie/Referer 等特殊请求上下文。

HTMLAudio 本身不能像普通 Go HTTP 请求一样任意设置所有请求头。遇到必须依赖特殊请求头、跨域策略或浏览器不允许的鉴权方式时，先验证 WebView 行为，再评估受控 Go 流代理；不得假定浏览器必然能直接播放，也不得绕过安全策略。

## 4. 唯一开发顺序与分阶段实施计划、验收门

**本节是音乐模块改造唯一有效的开发顺序和验收依据。** 开发必须严格按 A→B→C→D→E→F→G 推进；本文其他章节只能记录架构约束、审计证据、代码状态和历史结果，不得另行定义阶段、改变先后顺序或增加平行路线。若其他章节与本节冲突，以本节为准。

执行规则：
- **A：生产脚本导入与生命周期、B：统一 action 调度与歌曲模型接线、C：播放 URL/音质回退/缓存闭环均已完成对应阶段完成门。** 当前唯一下一阶段为 D；本轮 C 完成后不自动启动 D。
- 测试随功能开发同步编写；每项完成时集中运行该项测试及相关回归测试。CI 运行期间可继续不冲突的工作，但完成门必须取得对应 CI 结果。
- CI 失败必须读取具体日志、修复根因并重跑；提交成功或 CI 启动不等于验收完成。
- 不因单个边缘测试无限拆分开发；同类问题集中修复。第三方脚本许可只限制复制/分发对应脚本，不阻塞自建 fixture、兼容层和合法脚本导入流程。
- 状态只能依据代码、测试、CI 或设备实测证据更新；不得把基础 runtime 测试通过写成音乐模块迁移完成。

### A. 生产脚本导入与生命周期（已完成）

**当前代码状态：已实现并通过定向 CI 验收。** 生产 runtime 已从 `NewSourceManager(nil)` 改为复用现有 `config.Manager`，从 `config/local/lx_sources.json` 恢复用户脚本；MusicService 已暴露导入、列表、启停和删除 API，音乐设置页已提供 JS 文件/源码导入及管理界面。

**工作内容**
- 已复用 `config.Manager` 的 `config/local` 持久化目录，不引入平行存储根目录；新增有大小上限的 JSON 原子读写和损坏文件隔离。
- 已接入 production SourceManager；导入脚本经统一 `NewSourceManagerWithScripts` 校验，恢复时逐个验证，单个损坏/哈希不匹配的脚本会被禁用并记录错误，不拖垮其他脚本。
- 脚本记录包含 SHA-256 标识、名称、版本、作者、描述、主页、源码、扩展 action 声明、启用状态和错误状态；重复导入拒绝静默覆盖。
- 已接入导入、重启恢复、禁用、启用、删除、损坏隔离和 runtime 关闭释放；设置页可选择 `.js` 文件或粘贴源码。
- 所有脚本仍经现有受控 Host API，沿用 SSRF、超时、并发和资源限制。

**完成门**
- production runtime 启动后能从持久化存储恢复已启用脚本；MusicService 提供脚本管理 API，已导入脚本可经 production runtime 的 `musicUrl` resolver 执行。完整搜索/详情等 action 调度属于 B 阶段，不提前计入 A。
- 无脚本时能力状态准确报告不可用；禁用/删除后脚本不再可调度，相关 runtime/请求资源被释放。
- 导入、元信息、重启恢复、URL 执行、启停、删除、损坏/哈希不匹配脚本隔离及关闭释放均有回归测试；QuickJS、production tag、desktop 和 Wails 前端构建 CI 全部通过（Run #544，2026-11-23）。

### B. 统一 action 调度与歌曲模型接线

**当前代码状态：已完成。** 生产脚本 action dispatcher 已接入单音源搜索、全局搜索、弹幕点歌搜索、详情、歌词和封面入口；设置页支持显式声明 search/detail/lyric/pic。SourceManager 按每个脚本 inited.sources 路由扩展 action 和原生 musicUrl，防止多个脚本跨音源误执行；原生 musicUrl 还必须出现在该音源原生 actions 声明中。最终提交 f38a21795e16cacc05ab05f9a9aeeeace726cb6a，CI Run #567 全部成功。

**工作内容**
- 明确区分洛雪原生协议/action（如在线音源 musicUrl）与 My-LiveHub 社区扩展 action（search/detail/lyric/pic），不得将扩展 action 冒充洛雪官方原生协议。
- 统一脚本 action dispatcher 已接入 MusicService 的单音源/全局/弹幕搜索、详情、歌词和封面入口；原生 musicUrl 仍走 SourceManager.ResolvePlaybackURL。
- SourceManager 从每个脚本的 inited.sources 读取音源范围与原生 action 声明：扩展 action 仅在脚本声明该音源后执行；原生 musicUrl 还必须由该音源原生 actions 明确声明。多脚本按音源路由，不支持的脚本跳过；没有适用扩展时回退既有 Go provider。
- 搜索结果映射为统一 music.Track/SearchPage，保留 source id、LX source key、SourceData、时长、封面等字段；详情结果合并回原 Track；歌词/封面按字符串契约返回。18 秒 action deadline、timeout/cancel 错误分类、错误链、多脚本隔离和调用取消均有测试。
- 自建最小 production fixture 验证 search/detail/musicUrl/lyric/pic 经 QuickJS → SourceManager → MusicService 的调用；未复制第三方脚本。

**完成门**
- [x] 合法导入脚本 production fixture 在 MusicService 路径声明并调用 search/detail/lyric/pic，并单独验证原生 musicUrl；QuickJS → SourceManager → MusicService 集成测试通过。
- [x] 搜索与详情结果统一映射/合并 music.Track；缺失能力、多脚本隔离、timeout/cancel 分类及调用取消测试通过。
- [x] 按音源范围路由 action；未声明/不支持当前音源时回退 Go provider；原生 musicUrl 与社区扩展 action 分离并验证原生声明。
- [x] CI Run #567：QuickJS-NG native build、deterministic runtime/music tests、production-tag tests、desktop music integration、Wails bindings、npm ci、frontend build 全部成功。

### C. 播放 URL、音质回退与缓存闭环

**当前代码状态：Go 侧阶段完成门已通过。** 已新增结构化 `PlaybackResource`（URL/source/track/quality、媒体 headers、MIME/codec、过期/刷新及代理标志），作为进程内字段挂接队列 Track（`json:"-"`）；统一 resolver 支持显式音质优先、已选音质次之、音源质量列表顺序回退，以及 TTL 缓存、源过期时间上限、失效和单次刷新。队列路径将结构化资源交给 `LoadResource` 资源感知引擎端口；确定性集成测试确认资源确实到达该端口，旧引擎 `Load` 兼容回退不计入完成。Go 侧 `HandlePlaybackResourceFailure` 校验 source/track/quality，并对同一失效 URL 幂等刷新。URL-only 原生 `musicUrl` 不伪造媒体 headers、MIME/codec、ExpiresAt 或代理需求。以上由 CI Run 45142573201（20/20 步骤）通过。D 已将结构化资源交给生产 HTMLAudio，并将浏览器 `error` 事件接到该回调；自动化测试已通过，真实目标 WebView 播放仍待设备验收。

**工作内容**
- [x] 将队列选中歌曲接入统一播放资源 resolver；复用现有 SourceManager 原生 `musicUrl`，按音源范围路由，不调用社区扩展 action 冒充原生解析。
- [x] 统一音质顺序：显式请求音质优先，其次 Track.SelectedQuality，再按音源返回的 Track.Qualities 顺序逐级尝试；失败后继续下一质量。
- [x] 增加有上限的进程内 TTL 缓存、源过期时间上限、命中、过期淘汰、显式失效和单次刷新解析；缓存键按 source/track/quality 隔离。
- [x] 定义结构化 PlaybackResource，表达 URL、source/track/quality、媒体 headers、MIME/编码提示、过期/刷新信息和代理需求；资源字段不进入 Track 普通 JSON，避免签名 URL 意外出现在队列事件/状态中。
- [x] Go 队列路径优先向实现 `LoadResource` 的资源感知引擎传递结构化资源；旧引擎 `Load` 仅为迁移期兼容，不计入 C 完成。Go 侧 `HandlePlaybackResourceFailure` 按失效资源身份幂等刷新，同一失效 URL 的重复媒体错误只执行一次重新解析。
- 原生洛雪 `musicUrl` action 的协议返回值仍是 URL 字符串；不得擅自把原生 action 改成 object，也不得把 `lx.request` 的 API headers 复制成媒体 GET headers。当前无显式媒体上下文来源时，`PlaybackResource.Headers` 保持为空，MIME/codec/ExpiresAt/proxy 等未知字段不伪造。
- D 已实现 HTMLAudio 消费 C 的 `PlaybackResource`，并将浏览器 `error` 事件携带当前 track/resource 交给 `HandlePlaybackResourceFailure`；剩余验收是目标 WebView 的真实播放与设备控制验证。
- 生产 URL 和请求继续受安全策略约束；媒体最终连接的 DNS pinning/redirect 由后续受控媒体传输保证。

**完成门**
- [x] Go 队列路径解析并持有结构化资源；资源感知引擎端口在确定性集成测试中实际收到该资源。旧引擎兼容回退不算通过。
- [x] 音质回退、缓存命中、TTL 到期、显式失效、单次刷新解析、身份隔离、headers 深拷贝、确定性容量淘汰及 URL/header 安全拒绝测试通过 CI Run 45142573201。
- [x] Go 媒体失败回调校验 track/source/quality 身份，并对同一失效 URL 幂等刷新；HTMLAudio `error` 事件已接入回调并通过自动化测试，真实 WebView 实播由 D 阶段设备验收。
- [x] 媒体上下文契约明确区分脚本 API headers 与媒体 GET headers；当前原生 `musicUrl` 字符串协议不提供媒体 headers，URL-only 资源不会伪造 headers/MIME/codec/过期/代理信息。
- [x] C Go 侧资源解析、资源感知端口、幂等失败刷新、上下文边界测试及全部 CI 通过（Run 45142573201，20/20）。D 仍须按唯一顺序接入 HTMLAudio 消费和实际事件。

### D. HTMLAudio PoC 与正式播放桥接

**当前代码状态：代码与自动化 CI 子门通过；D 阶段整体仍未完成。** `main.go` 生产构造路径已通过 `AttachHTMLAudioPlayback` 切换到 session-aware HTMLAudio PlaybackEngine；单例 Audio、Wails 命令/事件桥、播放控制、媒体失败刷新和切歌取消已接通。CI Run 45149682218 全部通过；Windows 目标 WebView 的真实音源可听播放及设备信息仍待实测。

**工作内容**
- 在现有 Wails WebView/Vue 页面创建唯一、可复用的 HTMLAudio 实例，使用 C 阶段真实脚本解析资源验证可听见播放。
- 定义 Go MusicService 与前端 Audio 的 load/play/pause/resume/seek/setVolume/stop/snapshot 命令及事件契约。
- 为播放会话分配递增 generation/session ID，丢弃旧会话迟到的 URL 结果和音频事件。
- 处理自动播放拒绝、网络中断、HTTP 错误、格式不可解码、ended/error 和设备输出异常。
- 记录 Windows/WebView 版本、资源格式、请求响应特征及实播结果。

**完成门**
- Windows 目标 WebView 至少一个真实来源可听见播放，并完成基本控制。
- 快速切歌、暂停/恢复、跳转、音量、停止、错误和迟到事件处理通过测试。
- 不启动洛雪客户端或外部播放器；相关构建与 CI 检查通过。设备实测证据单独记录。

### E. 媒体传输适配与旧引擎替换

**当前代码状态：未完成。** 生产主窗口已切换到 HTMLAudio；旧 `LXPlaybackEngine` 实现仍保留在代码库及测试/兼容构造中。媒体 headers、Range、redirect、seek 和最终连接安全尚未端到端验收。

**工作内容**
- 实测 HTMLAudio 直连对 CORS、媒体请求头、Range、重定向、seek 和跨域的支持情况。
- 只有直连无法满足来源请求上下文时才实现受控 Go 流代理；代理必须支持 Range、取消、逐跳 redirect 校验、连接 pinning 和流式转发，并复用现有 SSRF/资源限制策略。
- 禁止整曲预下载；代理不得绕过 DNS 校验、重定向限制、请求大小/并发限制。
- 将 MusicService 默认播放实现切换为 HTMLAudio 桥接端口，移除生产播放路径对 LXPlaybackEngine、洛雪本地 API 状态轮询和 `lxmusic://` 启动协议的依赖。
- 保留旧配置迁移兼容，确保既有 music.json 其他设置不丢失。

**完成门**
- 不安装/启动洛雪客户端时，现有搜索、队列、播放控制、自动切歌可完整工作。
- 按实际需要验证媒体 headers、Range、seek、redirect、取消和错误恢复；旧播放依赖从生产链路移除。
- 旧配置迁移回归测试及相关 CI 通过。

### F. 歌词、封面、队列与多视图整合

**当前代码状态：部分完成。** 现有 Go 歌词/队列能力仍在，但 QuickJS 社区 lyric/pic action 与 HTMLAudio 真实播放时钟尚未完整接通。

**工作内容**
- 接通脚本 lyric/pic action 与现有歌词、封面服务和缓存。
- 歌词进度以 HTMLAudio 真实 currentTime 为准，验证原文、翻译、逐行/逐字时间戳格式与同步策略。
- 切歌时取消旧歌词/封面任务，清理旧缓存引用，防止旧结果覆盖当前歌曲。
- 验证 Go 队列顺序、插队、删除、重复曲目策略、播放模式和 ended 自动切歌。
- 主播放器、迷你播放器、独立歌词窗口共享同一播放状态和同一个 Audio 实例。

**完成门**
- 歌词与实际播放时间同步，无明显累计漂移；封面/歌词失败可诊断。
- 队列操作、自动切歌和播放状态一致；多窗口/多视图无重复播放或状态分叉。
- 切歌后旧歌词和旧事件不会残留；相关测试与 CI 通过。

### G. 安全、稳定性、性能与最终验收

**当前代码状态：部分完成。** QuickJS HTTP 安全边界、SSRF/DNS rebinding、redirect、headers、资源限制和部分生命周期测试已有；真实媒体连接、长时间播放、进程内存增量和设备稳定性未验收。

**工作内容**
- 对脚本隔离、超时/取消、SSRF/DNS rebinding、redirect、headers、请求/响应体、并发和资源释放做最终回归审查；所有生产网络请求复用受控出口。
- 完成搜索缓存、播放 URL 缓存、歌词缓存容量/TTL/失效策略和有限重试。
- 验证停止/切歌后请求取消、Audio 事件监听清理、对象引用释放和异常恢复。
- 分别测量 My-LiveHub 主进程、WebView renderer 的基线与音乐模块增量，覆盖空闲、搜索、播放、歌词+队列、多音源并发和停止释放。
- 在目标 Windows 设备连续播放至少 2 小时，记录稳定期内存、播放错误、切歌及停止后的内存回落。
- 全仓检查旧洛雪本地 API、`lxmusic://`、外部播放器进程和旧引擎残留，记录最终验收结论。

**完成门**
- 安全、缓存、取消、资源释放和异常恢复测试通过；无无限重试、无界缓存或旧会话资源泄漏。
- 提供进程级原始内存数据和增量计算，达到既定目标；不能用估算值冒充实测值。
- Windows 目标设备连续播放 2 小时无持续性内存增长，播放/切歌/歌词无明显卡顿。
- 相关 CI、构建和设备验收全部通过；My-LiveHub 可独立运行，无需安装或启动洛雪、MusicFree、mpv 等外部播放器。

## 5. 风险与决策规则

| 风险 | 应对方式 |
|---|---|
| WebView 不支持音源编码 | 先尝试音源音质回退；必要时评估受控流代理或明确不支持，不直接引入独立播放器 |
| HTMLAudio 无法设置所需请求头 | 验证浏览器限制和 CORS；评估 Go 代理并正确处理 Range/流式响应 |
| 浏览器自动播放限制 | 首次播放由用户手势触发，处理 play Promise rejection |
| 签名 URL 过期 | 失效后重新调用音源解析，限制重试次数 |
| Go 与 Vue 状态竞态 | session generation、单一状态权威和幂等事件处理 |
| WebView 内存归因困难 | 记录主进程和渲染进程基线/增量，报告测量方法 |
| 旧配置升级 | 保留迁移兼容，不把历史 LX API 配置作为新播放器启动条件 |

决策优先级：先保证目标 WebView 中真实音源能可靠播放，再解决特殊请求头/格式兼容；始终优先复用现有业务层和 WebView，不因单个音源问题扩大为独立播放器架构。

## 6. 开发纪律

1. 始终在 `feat/lx-js-runtime-quickjs-poc` 分支推进，除非用户明确要求更换分支。
2. 每次开发前先核对本文当前阶段和仓库实际代码，避免重复实现或跳阶段。
3. 一次聚焦当前阶段的完整验收门；必要的后续阶段问题先记录，不提前扩散实现。
4. 每次提交后检查对应 CI；失败时读取具体日志、定位根因、修复、再次运行。
5. CI 通过不等于真实播放通过；涉及 WebView、音频设备和内存的验收必须有实际运行证据。
6. 完成阶段后更新本文“进度记录”，写入提交 SHA、CI 结果、测试命令/设备和未解决问题。
7. 不得把计划、估算、mock 测试或仅编译成功描述为真实播放/性能验收通过。

## 7. 进度记录

**本节只记录第 4 节唯一开发顺序 A→G 的当前状态，不再保留旧的阶段 0—9 路线。** 当前状态必须与第 4 节一致；发生变化时以代码、测试和 CI 证据同步更新两处。

| 第 4 节阶段 | 当前状态 | 证据/备注 |
|---|---|---|
| A. 生产脚本导入与生命周期 | **已完成** | 已接入 `config/local/lx_sources.json`、production SourceManager、MusicService 管理 API 和音乐设置页；QuickJS/desktop/Wails bindings/前端构建 CI Run #544 成功 |
| B. 统一 action 调度与歌曲模型接线 | **已完成** | 生产 search/detail/lyric/pic 调度、按脚本音源范围路由、原生 musicUrl 声明校验、Track 映射/合并、缺失能力回退、错误/取消分类、多脚本隔离均已实现；CI Run #567 全部通过 |
| C. 播放 URL、音质回退与缓存闭环 | 部分完成 | resolver 与安全校验有基础；生产脚本装载及队列播放链路未闭环 |
| D. HTMLAudio PoC 与正式播放桥接 | 代码/CI/Windows PoC 构建通过；目标设备实播待验收 | 无目标 WebView 真实播放验收 |
| E. 媒体传输适配与旧引擎替换 | 未开始 | 旧引擎代码/兼容构造仍保留；媒体代理尚未完成 |
| F. 歌词、封面、队列与多视图整合 | 部分完成 | Go 侧已有基础；脚本扩展 action 和 HTMLAudio 时钟未接通 |
| G. 安全、稳定性、性能与最终验收 | 部分完成 | HTTP/SSRF 等已有测试；真实媒体连接、长时间播放及设备内存数据未验收 |

### 当前唯一下一步

A 已通过 CI Run #544，B 已通过 CI Run #567 并达到全部完成门。当前唯一执行阶段为 **C：播放 URL、音质回退与缓存闭环**；不得跳到 D。

**以下“阶段 0”标题及内容均为历史审计记录，保留其当时的源码证据和技术结论；它们不再代表当前阶段、开发顺序或下一步。**

### 阶段 0 已确认的关键调用链（2026-11-23）

本轮继续检查后确认：

1. `MusicView.vue` 的搜索结果通过 `Enqueue` / `PlayTrackNow` 交给 Go；前端没有直接的 Audio 播放实现。
2. `MusicService.playNext()` 调用 `resolveAndStartLXTrack()`，后者位于 `music_auto_switch.go`，会按已启用音源优先级尝试候选歌曲，并直接调用 `s.engine.Load(ctx, candidate)`。
3. 自动换源当前不是独立的 URL 解析服务：候选 Track 被交给旧 LX 播放引擎后，由旧播放路径委托洛雪客户端完成实际播放与确认。新方案必须把“解析播放 URL”从“执行播放”中拆出来，不能只把 `LXPlaybackEngine` 改名。
4. `MusicService.watchPlayback()` 每 500ms 读取一次 `engine.Snapshot()`，依赖真实播放状态、停止状态和剩余时长判断歌曲结束；歌曲结束后由 Go 推进 My-LiveHub 队列，队列为空时当前代码会尝试调用洛雪自己的 `NextTrack`。新方案必须改成以 HTMLAudio 的 `ended` 事件为可靠结束信号，并移除外部播放器队列回退。
5. `Pause/Resume/Stop` 当前直接调用 Go PlaybackEngine。新方案需要由前端 HTMLAudio adapter 执行，并将实际结果/事件回传。
6. `GetLyrics(track)` 当前通过 PlaybackEngine 扩展接口读取洛雪客户端 `/lyric-all`、`/lyric`，并先用当前外部播放器 Snapshot 核对歌曲。歌词必须改为按 Track/音源独立获取，不能再依赖“洛雪当前正在播放哪首歌”。
7. 主音乐页通过 `GetCachedPlaybackStatus()` 读取 Go watcher 缓存；独立歌词窗口也以约 500ms 频率轮询状态，并用本地时钟每 30ms 外推歌词位置。接入 HTMLAudio 后，应优先共享同一播放状态快照/事件，避免 Go watcher 与前端 Audio 各自成为状态权威。
8. `MusicConfig` 当前版本为 14，包含 `LXAPIEndpoints` 和默认 `http://127.0.0.1:23330`。移除旧播放路径时必须做向后兼容迁移，并调整设置 UI 中“洛雪客户端连接”文案。

### 阶段 0 的接口设计结论（待 PoC 验证后定稿）

建议采用“Go 业务服务 + 前端 Audio 端口”的边界，而不是让 Go 直接实现浏览器音频设备：

- Go 侧：`ResolvePlaybackResource(ctx, track, quality) (PlaybackResource, error)`，只负责调用当前音源适配器/QuickJS-NG 解析资源；不启动播放器。
- Go 侧：播放会话协调器负责 session ID、当前 Track、队列、历史、自动切歌和错误状态。
- 前端侧：单例 HTMLAudio adapter 执行 load/play/pause/resume/seek/setVolume/stop，订阅真实媒体事件。
- 桥接事件：`playback:command`（Go → Vue）和 `playback:event`（Vue → Go）或等价的 Wails 绑定方法；具体机制在阶段 1 PoC 中按当前 Wails 版本验证后确定。
- 资源模型至少携带 URL、source ID、track ID、quality、MIME/编码提示、过期/重解析信息和受控请求上下文。敏感签名 URL 不写日志。
- 每次 load/stop/切歌递增 session ID；所有异步解析结果和媒体事件都必须校验 session ID。
- HTMLAudio `ended` 只触发一次队列推进；Go 队列是唯一业务队列，绝不回退到第三方播放器队列。
- 歌词由音源服务按 Track 获取，歌词位置以 HTMLAudio currentTime 为准。

**尚未完成的阶段 0 项目**：逐个确认当前 QuickJS-NG/用户源运行时代码中 `musicUrl` action 的真实调用入口、结果结构和请求头处理；采集目标三音源的真实 URL/格式/请求约束；确认 Wails 当前版本下可靠的前端命令/事件桥接方式。因此阶段 0 仍保持“进行中”，不能开始声称阶段 1 已验收。


### 阶段 0 第二轮审查：QuickJS 解析契约、音源脚本与 Wails 通信（2026-11-23）

#### A. QuickJS-NG 已有的真实解析链

已确认当前分支不是“只有 QuickJS PoC”：

- `MusicService.ResolvePlaybackURL(track, quality)` 已存在，调用 `music.NewUnifiedSourceService(searcher, runtime).ResolvePlaybackURL(...)`。
- `UnifiedSourceService` 将统一 Track 转换为 LX 脚本所需的 `{ musicInfo, type }`，保留 songmid/hash/media_mid/album_mid 等平台原生字段。
- `lxScriptRuntime` 接口已经有 `ResolvePlaybackURL(ctx, source, info) (string, error)`。
- `lxruntime.SourceManager.ResolvePlaybackURL` 按三个内置脚本（星澜、HelloWorld、幻音）依次初始化/调用，某个脚本失败后尝试下一个脚本；每个脚本拥有独立持久 QuickJS Runtime。
- 三个脚本的动作入口均为 `musicUrl`。当前 SourceManager 最终只接受非空 HTTP(S) **字符串**，并校验 scheme/host。
- `lx.request` 宿主请求已支持 method、timeout、headers、body、form/formData、取消和响应头；这些 headers 用于 QuickJS 脚本向 API 请求数据，不会自动附加到最终 HTMLAudio 对播放 URL 的 GET 请求。
- `productionMusicLXRuntime` 只在 `lxproduction && cgo` 构建标签下启用；其他构建返回 nil。生产构建链必须保留该约束并在阶段 4 真实验证。

#### B. 三个内置脚本与平台覆盖的准确含义

当前 SourceManager 内置的三个脚本是三个**脚本包/解析后端**，不是三个音乐平台：

- 星澜脚本：聚合 QQ、网易云、酷我、酷狗、咪咕及汽水等平台，并有多后端 URL 回退、质量映射、缓存；脚本中可配置 QQ/网易 Cookie，部分 API 请求使用 Referer/User-Agent 等请求头。
- HelloWorld 脚本：基于配置的 API 源和质量回退解析 URL。
- 幻音脚本：实现 QQ、酷我、网易云、咪咕等平台的 URL 解析入口。
- Go 搜索适配器当前有五个平台：`tx/kw/kg/wy/mg`。不要把“五个平台”“三个内置脚本”混为一谈，也不要把脚本内置的多个 API 后端当成独立音源平台。

目前三个脚本返回的是 URL 字符串；仓库尚无对这三个脚本、五个平台在目标 Windows WebView 上的完整真实 URL、MIME/编码、Range、重定向、Cookie/Referer 播放要求矩阵。因此不能宣称已完成真实格式兼容性验证。

#### C. 已发现的关键架构缺口

1. **解析服务已有，但尚未接入播放主链路。** `MusicService.ResolvePlaybackURL` 可供调用；当前 `playNext → resolveAndStartLXTrack → engine.Load` 仍把 Track 交给 `LXPlaybackEngine`，并没有先调用这个 URL resolver。阶段 3 必须复用现有 resolver，不应另写一套 QuickJS 调用。
2. **播放 URL 资源模型过窄。** 目前 resolver 返回 `string`，丢失媒体响应所需的 headers、MIME/编码提示、过期信息及来源信息。脚本的 API 请求 headers 与媒体 GET headers 是两个不同概念。阶段 0/1 应先定义 `PlaybackResource`，默认不携带可写入日志的敏感 Cookie/签名参数。
3. **HTMLAudio 请求头限制需要实测。** 如果最终媒体 GET 必须携带 Referer/Cookie/自定义 header，单纯把 URL 赋给 Audio.src 可能无法播放。阶段 4 需要依据真实响应决定直连、音源降级或 Go 流代理；代理必须支持 Range、取消、Content-Type、Content-Length/分块传输和重定向语义。
4. **当前歌词确实被旧播放器绑定。** `GetLyrics` 依赖 `PlaybackEngine.GetCurrentLyrics` 和 Snapshot 前后核对；歌词独立于播放执行器的按 Track 获取接口尚未存在。
5. **自动换源与第三方队列深度耦合。** `PlayNext` 的队列空分支和 `Next/Previous` 都会回退到 `LXPlaybackEngine.NextTrack`；新播放器必须让 My-LiveHub 自己的队列成为唯一队列，队列为空时进入 idle，不再推进洛雪客户端队列。

#### D. Wails v3 前后端通信方案

仓库使用 `github.com/wailsapp/wails/v3 v3.1.0`，前端已导入 `@wailsio/runtime`，Go 侧通过 `application.New(...)` 注册 `MusicService` 绑定。

Wails v3 官方 Events API 提供 Go `app.Event.Emit(name, data)` 向前端发事件、前端 `Events.On(name, callback)` 订阅并返回清理函数；前端到 Go 的业务调用继续使用已生成的 Service Bindings。参考：
- https://v3.wails.io/reference/events/
- https://wails.io/reference/frontend-runtime/

据此建议 PoC 验证以下最小通信协议（事件名称是拟定契约，不代表已实现）：

- Go → WebView：`music:playback-command`，payload 含 `session_id`、`command`、`resource`、`track`；command 为 load/play/pause/resume/seek/set_volume/stop。
- WebView → Go：通过绑定方法 `ReportPlaybackEvent(event)` 回报媒体事件；事件含 `session_id`、`event_type`、`current_time_ms`、`duration_ms`、`error`。
- Go → WebView：`music:playback-state` 广播权威快照，供主音乐页、桌面歌词窗和其他 UI 视图共享。
- Vue 的 HTMLAudio adapter 单例监听 Go 命令，所有事件回报带 session ID；旧 session 的事件一律忽略。
- HTMLAudio 的 `ended` 由 Go 会话协调器幂等处理并推进 My-LiveHub 队列；不能由多个前端窗口分别推进队列。
- 需在阶段 1 用真实 Wails 窗口验证窗口创建/运行时就绪后事件订阅、事件广播和窗口重建行为；不能只用普通浏览器 mock 代替。

#### E. 阶段 0 当前结论

已完成：主链路、现有 QuickJS URL resolver、三个脚本动作入口和返回值、平台/脚本数量边界、Wails v3 事件通信候选方案、旧歌词/队列耦合点。

仍未完成（必须有仓库/运行证据）：
- 从三个内置脚本分别为五个平台获取可复现的真实播放资源，并记录 URL 脱敏后的 host/path 特征、HTTP 状态、Content-Type、Accept-Ranges、Content-Length/Transfer-Encoding、重定向和播放请求头要求。
- 在目标 Windows WebView 中用至少一条真实资源验证是否能播放，以及必要请求头是否造成阻断。
- 以 Wails v3.1.0 实际构建验证 Events API 的 Go 初始化入口、事件订阅与清理，确认主窗口和歌词窗口都能收到同一播放状态。
- 根据真实媒体请求约束定稿 `PlaybackResource` 字段，以及是否需要 Go 流代理。

因此阶段 0 仍为进行中；上述剩余项完成后才能通过阶段 0 验收并进入阶段 1 PoC。


---

## 8. 架构约束修订与重新制定的开发基线（2026-11-23）

本节为本方案的**最新且优先级最高的架构决策**。若前文旧阶段描述与本节冲突，以本节为准。

### 8.1 已确认的核心决策

**继续使用洛雪 JS 音源脚本，通过模拟洛雪运行时环境实现搜索、播放等功能。** 不把 QuickJS-NG 降格为只执行少量内置脚本的工具，也不把三个内置脚本当成固定的三个音源实现。

完整链路：

`导入洛雪 JS → 洛雪运行时兼容层 → QuickJS-NG 执行 → 搜索/详情/播放 URL/歌词等 action → Go 统一音乐服务 → HTMLAudio 播放`

职责边界：

- **洛雪 JS 脚本**：承载具体音源平台适配逻辑、搜索/详情/播放 URL/歌词解析逻辑。支持内置脚本和用户导入脚本。
- **洛雪运行时兼容层**：模拟洛雪脚本运行时环境，提供脚本依赖的全局对象、`lx` 宿主 API、网络请求、存储、事件、生命周期及必要的兼容行为。
- **QuickJS-NG**：提供 JS 引擎、上下文和脚本执行能力；它本身不等于洛雪运行时兼容层。
- **Go 音乐服务**：管理脚本注册与调度、统一 Track/搜索结果、队列、播放会话、缓存、权限和错误处理；复用现有 `MusicService.ResolvePlaybackURL` / `UnifiedSourceService`，避免重复造轮子。
- **HTMLAudio**：只负责媒体资源实际加载、解码和播放。不得承担音源 API、签名算法或洛雪脚本执行。
- **受控 Go 媒体代理**：仅当真实资源要求 HTMLAudio 无法提供的请求头/鉴权/Range 能力时启用；不是默认整曲下载器。

### 8.2 洛雪运行时兼容范围

兼容目标是**洛雪 JS 音源脚本运行环境和脚本 API 行为**，不是只让当前三个内置脚本通过。按实际脚本依赖建立矩阵，至少检查：

1. 脚本文件导入、格式/元信息解析、脚本 ID/名称/版本及配置项。
2. JS 全局对象、模块加载方式、运行上下文和脚本生命周期。
3. `lx` 对象及其实际使用的 API：请求、响应、存储、日志、通知、事件/回调、定时能力等。
4. 请求参数与响应语义：headers、method、body、form、超时、取消、重定向、文本/JSON/二进制响应。
5. 洛雪脚本 action 调用协议、参数结构、返回值结构和错误语义。
6. 搜索、歌曲详情、播放 URL（`musicUrl`）、歌词等能力。
7. 多脚本隔离、持久上下文、并发调用、超时和资源上限。
8. 真实第三方脚本导入测试；内置脚本通过不能替代第三方脚本兼容性证据。

每项标注：**已实现 / 部分实现 / 未实现 / 不适用**，附代码位置、测试用例和兼容差异。没有证据的能力不得标为已兼容。不得宣称百分之百兼容，除非有完整规范和测试覆盖支持。

### 8.3 阶段顺序引用

阶段顺序、依赖关系、工作内容和完成门统一以第 4 节为准。本节不维护第二份阶段表。

### 8.4 开发顺序引用

所有实施任务按第 4 节唯一顺序推进；本节不另行规定阶段顺序。

### 8.5 当前状态校正

截至本次文档修订：

- 已确认项目继续使用洛雪 JS 脚本，QuickJS-NG 作为执行引擎，必须通过兼容层模拟洛雪运行时环境。
- 已确认现有代码具备部分 QuickJS/LX runtime、三个内置脚本的 `musicUrl` URL 解析链和五个平台 Go 搜索适配器；这**不等于**完整洛雪运行时兼容，也不等于通用 JS 导入能力已经验收。
- 现有 `MusicService.ResolvePlaybackURL` 尚未接入当前旧播放器播放主链路；后续必须复用并扩展资源模型。
- HTMLAudio 尚无目标 Windows WebView 的真实播放验收证据。
- 当前实施状态以第 4 节 A→G 和第 7 节进度记录为准；历史阶段 0—9 仅用于解释此前审计过程，不再作为当前路线或状态依据。

**本次修订只更新架构决策和开发计划，没有声称已经完成运行时兼容、第三方脚本导入或 HTMLAudio 实播。**

---

## 9. 可行性审查结论与开发前置门（2026-11-23）

本节记录对当前分支实现和本方案的可行性复核，优先级高于前文尚未细化的阶段描述。结论区分“技术路线可行”与“当前代码已经具备交付条件”，不得将两者混为一谈。

### 9.1 总体结论

**总体技术路线可行，但当前方案尚不能不经前置收敛就直接进入大规模功能开发。**

可行性依据：
- 仓库已有 QuickJS-NG CGo 执行运行时、部分洛雪宿主环境、lx.request HTTP 桥接和 action dispatch，可作为兼容层的实现基础。
- 已有 SourceManager、持久脚本 Runtime 和三个嵌入式 JS 脚本的 musicUrl 调用链；已有 MusicService.ResolvePlaybackURL / UnifiedSourceService，无需另起一套 URL 解析器。
- Go 音乐服务、统一 Track、队列和 Vue/Wails 页面已经存在；目标架构可以在现有业务层上演进。
- Wails v3.1.0 与前端运行时具备事件/绑定通信能力，Go 协调器与单例 HTMLAudio 的边界在技术上成立。

限制与结论：
- 现有实现已接入生产用户脚本导入/生命周期和 B 阶段 search/detail/lyric/pic action 调度，但仍不是完整洛雪 JS 运行时；未覆盖的宿主 API 继续按兼容矩阵推进。
- 原生洛雪 musicUrl 与 My-LiveHub 扩展 action 已明确分离；搜索/详情/歌词/封面在脚本声明相应扩展能力时由 QuickJS 执行，否则按既有 Go provider 规则回退。
- 原生 musicUrl 目前返回 URL 字符串；统一 resolver 将其包装为 PlaybackResource，但不会伪造媒体 headers、Cookie/Referer、MIME/codec 或 proxy 元数据。
- C 结构化资源已接入生产 HTMLAudio 主播放链路；浏览器错误事件可触发幂等 URL 刷新。媒体 headers、Range、redirect/DNS pinning 仍需 E 阶段受控传输处理。
- D 的生产桥接、Go 自动化测试、Wails bindings、前端构建和 Windows PoC 可执行文件构建均已通过 CI；目标 Windows/WebView 的真实音源可听播放和设备控制仍未取得实测证据。
- A/B/C 已完成；D 代码/CI 子门已完成，当前唯一执行阶段仍为 D，停止条件是目标设备实播及控制验收。不得进入 E。

因此：**架构方向通过可行性审查；A/B/C 已完成，D 已接通生产 HTMLAudio 并通过自动化 CI，当前仍等待目标 Windows/WebView 实播验收。**

### 9.2 关键缺口及处理决定

| 编号 | 缺口 | 当前事实 | 必须采取的处理 |
|---|---|---|---|
| F-01 | 洛雪原生协议与搜索扩展边界 | 已核对的 action 调用链以 musicUrl 为主；现有五平台搜索在 Go provider | 保留洛雪原生脚本 API 兼容层；搜索统一由 JS 脚本执行的需求，采用明确标识的 My-LiveHub 扩展 action（例如 search / detail / lyric），不得伪称这些是洛雪原生 action。对原生脚本缺少扩展 action 的情况提供清晰能力声明，不静默回退到 Go 五平台实现 |
| F-02 | 用户脚本导入与管理 | A 阶段已接入 `config/local/lx_sources.json`、production SourceManager、MusicService 管理 API 和设置页 | 已完成导入、元信息/hash 校验、持久化恢复、启停/删除、损坏隔离及关闭释放；后续只处理 action 调度和扩展能力，不重复建设脚本管理体系 |
| F-03 | 洛雪运行时兼容矩阵 | 已实现部分全局对象、lx API、请求桥接；各 API 的真实覆盖程度仍需逐项核验 | 建立 API 级矩阵，记录状态、代码位置、协议来源、测试和差异；按真实第三方脚本依赖补齐，不能仅凭符号存在就标为兼容 |
| F-04 | 播放资源结构 | 当前 resolver 返回 URL 字符串 | 将返回契约升级为结构化 PlaybackResource，至少包含 URL、source/track/quality、媒体 headers（敏感字段受控）、MIME/编码提示、过期/刷新信息、代理策略和脱敏元数据 |
| F-05 | 媒体传输能力 | HTMLAudio 不能任意设置请求头，真实媒体要求尚未采样 | 先采集真实资源的响应和请求限制；优先直连或脚本音质回退，确有必要才实现支持 Range/取消/重定向/流式响应的受控 Go 代理 |
| F-06 | 导入脚本安全 | QuickJS 无直接 OS API 不代表网络能力安全 | 对 lx.request 实施 SSRF 防护（回环、私网、链路本地、云元数据地址及 DNS 解析后地址校验）、协议/端口策略、超时、响应体上限、并发配额、重定向逐跳校验；对 JS 执行设置内存/栈/时间限制、脚本隔离、取消和资源回收 |
| F-07 | 搜索/详情/歌词契约 | 当前搜索是 Go provider；歌词路径仍依赖旧 PlaybackEngine | 先定义 JS action 输入/输出 schema、分页/排序、错误码和 Track 映射；歌词按 Track/source 获取，不依赖外部播放器当前曲目 |
| F-08 | 并发和生命周期 | SourceManager 的全局 dispatch 锁可能限制并发；脚本持久上下文需定义 | 明确每脚本串行还是并行、跨脚本并行策略、上下文线程归属、取消传播、卸载等待和锁粒度；用并发/竞态测试验证 |
| F-09 | 生产构建约束 | QuickJS 生产实现受 lxproduction && cgo build tag 控制 | 验证开发、CI、Windows 生产构建的 tag/CGO 配置一致；非生产 fallback 不得被误判为运行时正常可用 |

### 9.3 必须冻结的协议决策

1. **脚本 action 分层**：先按官方桌面端自定义源文档和实现源码确认原生 action 的精确范围；不得将洛雪桌面端内置 SDK 的搜索/详情能力误认为自定义源 action。My-LiveHub 扩展 action 是否需要、包含哪些能力，必须由完整源码审计和项目目标差异共同决定，审计前不预设 search/detail/lyric 扩展。
2. **搜索执行权**：按项目已确认的目标，搜索、详情、播放 URL、歌词由 JS 音源脚本执行；Go 负责调度与统一模型。现有 Go 五平台 provider 只能作为明确的兼容/迁移选项，不能在声称“脚本搜索”时暗中承担主搜索。
3. **用户导入模型**：内置与用户脚本使用同一个加载器、运行时工厂、权限系统和 action 调度器；禁止为内置脚本单独开特权执行路径。
4. **播放资源模型**：统一结构化返回值；媒体 GET headers 与脚本 API 请求 headers 分开保存和授权。敏感 Cookie/Token 不进入日志、普通事件广播或不可信前端持久存储。
5. **安全默认拒绝**：导入脚本网络请求默认受策略约束；拒绝本机/内网/元数据地址访问，重定向重新验证目标地址；限制响应大小、执行时长、并发和内存。
6. **兼容承诺边界**：以 API 兼容矩阵和真实第三方脚本测试报告声明兼容范围，不使用“完全兼容洛雪”作为没有证据的验收描述。

### 9.4 开发顺序引用

本节可行性审查发现的缺口按第 4 节唯一开发顺序处理，不另设平行阶段或前置路线。

### 9.5 可行性风险与技术验证清单

- QuickJS-NG 可承载脚本运行，但宿主 API 的语义、异步回调、二进制数据、模块加载和持久上下文必须通过真实脚本逐项验证；不能仅依据引擎本身支持 JS 就推导兼容。
- SSRF 防护必须覆盖 DNS rebinding 与重定向，不只做字符串域名黑名单；网络请求最终连接地址也要校验。
- QuickJS 的执行超时必须能中断实际执行，不能只在外层等待超时后遗留仍运行的脚本任务。
- 用户脚本配置/密钥需要按脚本隔离存储，导出日志和错误时脱敏。
- 播放代理若启用必须支持 Range 请求，否则长音频拖动/续播可能失败；不得默认将整曲读入内存。
- 真实播放测试必须在目标 Windows Wails WebView 与实际音频设备完成；普通浏览器或 mock 仅作为单元测试。
- 目标内存数字继续作为测量目标，当前没有实测数据，不构成可行性通过的证据。

### 9.6 本轮审查后的真实状态

| 项目 | 状态 | 说明 |
|---|---|---|
| 总体架构路线 | **可行** | 现有 QuickJS、Go 音乐服务和 Wails WebView 能作为实现基础 |
| 洛雪 JS 完整运行时兼容 | **未验收** | 目前只有部分宿主能力，需 API 矩阵和真实脚本证据 |
| 用户 JS 导入/管理 | **A 阶段已完成** | 已接入本地持久化、元信息/hash 校验、启停删除、损坏隔离、MusicService API 和设置页；B 阶段 action 调度已在生产链路完成 |
| JS 脚本搜索/详情/歌词/封面 | **B 阶段已完成** | 生产 search/detail/lyric/pic 入口、Track 映射、按音源路由、原生 musicUrl 分离、错误/取消和多脚本隔离均通过 CI Run #567 |
| 结构化 PlaybackResource | **C 阶段 Go 侧已完成** | 结构化资源、质量回退、缓存、资源感知端口、幂等失败刷新及 URL-only 上下文边界通过 CI Run 45142573201；HTMLAudio 实际消费在 D |
| HTMLAudio 目标 WebView 实播 | **未验证** | 无真实设备播放验收记录 |
| SSRF/脚本资源隔离 | **已有实现与专项测试，仍需后续实测** | DNS/连接 pinning、redirect、代理/TLS 收敛、请求资源限制已有测试；真实外部脚本长时间运行与 soak 尚未验收 |
| 当前执行项 | **A/B/C 已完成；D 代码与 CI 通过，目标设备实播待验收** | CI Run 45149682218 全部通过；Windows PoC artifact 由 Run 45150651612 构建 |

### 9.7 开发顺序与当前执行项

本节识别的协议、兼容性和 action 调度缺口，全部按第 4 节唯一开发顺序处理，不再设置阶段 0A—0F 等前置子路线。A、B、C 已完成；当前唯一执行阶段为 **D：HTMLAudio PoC 与正式播放桥接**，代码和 CI 子门已通过，目标设备实播仍待验收。



---

## 10. 洛雪官方源码审计记录（2026-11-23）

> 本节及其 10.x 子节是按时间追加的历史审计证据与修复记录。文中出现的“阶段 0/阶段 1”及“下一步”均指当时的历史计划，不代表当前开发路线；当前状态与唯一顺序统一以第 4 节和第 7 节为准。**本节不是当前实施计划，也不是审计完成声明**。审计以洛雪官方桌面端仓库 `lyswhut/lx-music-desktop` 的提交 `323e4bac505da428adae8db6b58039fb2b36b86d` 和官方文档仓库 `lyswhut/lx-music-doc` 的提交 `a033df5292dfb0b371043417ea06071df518c904` 为当前可复核基线。后续若切换源码版本，必须记录新旧版本差异。

### 10.1 已核实的官方资料

- 官方桌面端源码：`https://github.com/lyswhut/lx-music-desktop/tree/323e4bac505da428adae8db6b58039fb2b36b86d`
- 官方桌面端自定义源规范：`https://github.com/lyswhut/lx-music-doc/blob/a033df5292dfb0b371043417ea06071df518c904/docs/desktop/custom-source.mdx`
- 关键源码文件：
  - `src/main/modules/userApi/renderer/preload.js`
  - `src/main/modules/userApi/utils.ts`
  - `src/main/modules/userApi/main.ts`
  - `src/main/modules/userApi/index.ts`
  - `src/main/modules/userApi/rendererEvent/rendererEvent.ts`
  - `src/main/modules/winMain/rendererEvent/userApi.ts`
  - `src/common/types/user_api.d.ts`
  - `src/renderer/core/useApp/useInitUserApi.ts`
  - `src/renderer/utils/musicSdk/index.js`
  - `src/renderer/utils/musicSdk/api-source.js`
  - `src/renderer/core/music/utils.ts`
  - 平台 SDK：`src/renderer/utils/musicSdk/{kw,kg,tx,wy,mg}/index.js`

### 10.2 自定义源原生协议：已确认事实

1. 自定义源脚本是 UTF-8 JavaScript（官方文档允许 ES6+），文件头部注释通过 `@name`、`@description`、`@version`、`@author`、`@homepage` 提供元数据。
2. 桌面端向脚本暴露 `globalThis.lx`，包括 `version`、`env`、`currentScriptInfo`、`EVENT_NAMES`、`on`、`send`、`request` 和 `utils`。
3. 原生事件名为 `request`、`inited`、`updateAlert`。脚本通过 `lx.on(EVENT_NAMES.request, handler)` 注册处理器，并通过 `lx.send(EVENT_NAMES.inited, {openDevTools, sources})` 完成初始化。初始化事件前发生脚本错误会导致初始化失败。
4. 官方桌面端自定义源允许的 source key 为 `kw/kg/tx/wy/mg/local`。`inited.sources[source]` 声明 `type: "music"`、`actions` 和 `qualitys`；质量列表允许 `128k/320k/flac/flac24bit`。能力会被宿主端按 source 白名单过滤，而不是脚本声明什么就无条件开放什么。
5. **远程 source（kw/kg/tx/wy/mg）的自定义源 action 只有 `musicUrl`；`local` source 才允许 `musicUrl/lyric/pic`。** 这是官方桌面端 preload 的 `supportActions` 白名单与官方文档一致的结论。
6. `musicUrl` 入参为 `{type, musicInfo}`，其中 type 为所声明的质量标识；local source 的 type 为 null。返回值必须为 HTTP(S) URL 字符串。
7. local source 的 `lyric` 入参为 `{musicInfo}`，返回歌词对象；`pic` 入参为 `{musicInfo}`，返回 HTTP(S) 图片 URL。歌词字段为 `lyric/tlyric/rlyric/lxlyric`，宿主端对长度和类型执行校验。
8. `lx.request(url, options, callback)` 支持 method/headers/body/form/formData/timeout，回调提供 err、resp、body，并返回取消函数。桌面端实现使用 Needle；请求超时最大 60 秒。
9. `lx.utils` 官方桌面端提供 buffer、crypto（AES/RSA/随机数/MD5）和 zlib（inflate/deflate）能力。
10. `updateAlert` 每次脚本运行仅允许调用一次，日志最大 1024 字符。
11. 官方桌面端脚本导入会解析头部元数据、压缩保存脚本文本、按内容防重复；可加载、切换、删除脚本。当前实现以独立 Electron BrowserWindow 运行脚本，启用 contextIsolation、关闭 nodeIntegration、拦截导航和新窗口，并在卸载时清理该窗口的缓存/存储。
12. 宿主请求使用 requestKey 在主进程与脚本窗口之间关联；主进程 20 秒超时并支持取消。切换音源会关闭旧窗口并加载新脚本。

### 10.3 必须纠正的协议认知

**洛雪桌面端内置音乐 SDK 与“洛雪自定义源脚本协议”是两套不同层次的东西。**

- 官方桌面端内置平台 SDK（`src/renderer/utils/musicSdk/{kw,kg,tx,wy,mg}`）包含 `musicSearch`、`songList`、`leaderboard`、`hotSearch`、`comment`、平台歌曲详情、歌词、封面及播放 URL 等业务模块。
- 自定义源脚本协议并没有向远程 source 暴露通用 `search` 或 `detail` action；远程自定义源仅声明 `musicUrl`。自定义源的 `lyric/pic` 仅属于 local source action。
- 因此不能把“洛雪桌面端有搜索/详情/歌词功能”推导成“洛雪自定义源 JS 原生支持 search/detail/lyric actions”。
- 同样，不能在没有审计用户真实目标脚本样本、官方 SDK 与项目目标的情况下，直接把 `search/detail/lyric` 定义成 My-LiveHub 扩展 action 并称为兼容洛雪。
- 下一步需要继续审计官方内置 SDK 的搜索调用、数据模型和各平台模块；并确认用户所说“洛雪 JS 音源脚本负责搜索”所指脚本生态/脚本格式，是否属于官方桌面端自定义源协议，还是另一类可加载脚本。该差异必须通过真实脚本样本和源码调用链确认。

### 10.4 My-LiveHub 当前实现对照：已核实事实

- `app/internal/music/lxruntime/host_contract.go` 已实现部分桌面端 `lx` 对象、事件名称、source/action/quality 白名单、初始化状态及脚本元信息注入；但它是 Go/QuickJS 实现，不等于官方 Electron preload 行为完全等价。
- `app/internal/music/lxruntime/dispatch.go` 已实现 request action dispatch、Promise 结果轮询、`lx.request` 网络任务服务及回调交付。
- `app/internal/music/lxruntime/http_request.go` 已存在 Go HTTP 桥接、响应体上限（8 MiB）等实现；仍需逐项核对 DNS/重定向 SSRF、超时语义、二进制响应与官方 Needle 行为差异。
- `app/internal/music/lxruntime/source_manager.go` 通过 go:embed 固定内置三个脚本，按脚本隔离 QuickJS Runtime；`ResolvePlaybackURL` 当前只派发 `musicUrl`，返回 string。它不是用户可导入脚本管理器。
- `app/internal/music/search_lx.go` 的五个平台搜索由 Go 原生 provider 实现。此事实与“搜索必须由用户导入的洛雪 JS 脚本执行”的目标存在差距；但在明确目标脚本格式之前，不应擅自添加伪洛雪原生 search action。
- `app/internal/music/lyrics_lx.go` 存在 Go 原生平台歌词实现；它不等同于通过洛雪远程自定义源脚本的 lyric action（官方协议中远程 source 不支持该 action）。
- `app/internal/desktop/lx_script_runtime.go` 当前接口主要为 Start/ResolvePlaybackURL/Close；`MusicService` 仍将旧播放引擎作为默认 engine，脚本 URL resolver 也未构成 HTMLAudio 播放闭环。
- 当前结构化 PlaybackResource、通用用户脚本导入/管理、完整第三方脚本兼容及目标 Windows WebView 实播仍未验收。

### 10.5 审计未完成项（不得提前进入实现）

- [ ] 官方桌面端内置 SDK：搜索入口、平台搜索模块、搜索结果模型、详情/歌单/排行榜/热搜/评论调用链。
- [ ] 官方自定义源窗口生命周期：脚本注入顺序、事件监听清理、切源/删除/错误/超时的完整状态机；检查是否有未被当前摘要覆盖的宿主行为。
- [ ] `lx.request` 的 Needle 参数映射、响应 body/raw/bytes、重定向、代理、取消与异常语义。
- [ ] 官方脚本样本：收集多个真实第三方自定义源，核对其实际使用的全局 API、模块/Polyfill、异步模式、加密/二进制依赖；不得只以官方最小样例为依据。
- [ ] 官方 desktop 与 mobile 自定义源规范差异，明确项目只承诺 desktop 还是需要兼容 mobile 脚本子集。
- [ ] 音源配置、音质偏好、用户切源、搜索跨源匹配、其他音源匹配与 URL 获取之间的业务调用链。
- [ ] 与 My-LiveHub 当前 Go/QuickJS 实现逐个 API 对照，建立带源码位置、测试证据、状态和差异说明的矩阵。
- [ ] 真实脚本的媒体 URL headers/Referer/Cookie、有效期、重定向、Range 和 WebView 可播放性采样。
- [ ] 在以上项目完成前，阶段 0 保持进行中；不冻结新的扩展 action，不开始阶段 1 通用导入/搜索主链实现。

### 10.6 本轮审计状态

本轮已完成第一批官方规范和关键源码核对，并发现此前阶段 0A 的顺序需要调整：**先审计官方原生协议与内置 SDK 的边界，再决定协议是否需要扩展；不是先写扩展 action 再找源码证明。** 本轮只更新开发基线与审计记录，不表示完整审计或代码开发已经完成。


### 10.7 官方内置 Music SDK：搜索、跨源匹配与能力边界（第二批审计）

核对源码：`src/renderer/store/search/music/action.ts`、`src/renderer/utils/musicSdk/index.js`、`src/renderer/utils/musicSdk/api-source.js`、`src/renderer/utils/musicSdk/api-source-info.ts`、各平台 `{kw,kg,tx,wy,mg}/index.js` 和 `musicSearch.js`。

1. 搜索入口位于 renderer store 的 music search action。单源搜索调用 `music[sourceId].musicSearch.search(text, page, limit)`；全源搜索并发调用已启用的各平台 SDK，失败平台转为空结果，之后合并、去重、按关键词相似度排序。旧请求通过 key 检查丢弃过期结果。
2. SDK 中的平台源是模块对象，不是用户自定义源脚本。已确认平台对象包含 `musicSearch`、`songList`、`leaderboard`、`hotSearch`、`comment`、`getMusicUrl`、`getLyric`、`getPic`、部分源的 `getMusicInfo` / `getMusicDetailPageUrl` 等不同能力。
3. 当前默认平台列表为 kw/kg/tx/wy/mg/xm；bd 模块被导入但未列入默认在线源列表。跨源搜索 `searchMusic` 会跳过当前源和 xm，使用其他有 musicSearch 的平台；`findMusic` 再按歌名、歌手、专辑、时长和版本字符匹配排序，供播放失败后的跨源切换使用。
4. 各平台搜索响应结构不同，平台模块自行解析、去重、补充质量信息并映射为音乐对象；例如酷我解析 N_MINFO，酷狗按 Audioid/FileHash 去重并映射音质，咪咕解析 songResultData，网易云/QQ 使用各自请求和解析逻辑。统一对象是在平台模块解析后建立，而不是由用户自定义源协议直接返回统一 Track。
5. `api-source.js` 是另一个“可配置 API source”层：根据 apiSource 选择接口配置；当配置以 `user_api` 开头时，才路由到 `userApi.apis[source]`。当前 `api-source-info.ts` 内置配置数组为空（测试配置注释掉），因此不能将它误解为内置平台搜索脚本目录。
6. 平台 SDK 的 `getMusicUrl` 通过 `apis(source).getMusicUrl` 委托可选 API source；平台歌词、封面则多由各平台 SDK 自己实现。网易云/酷狗/咪咕/酷我/QQ 的歌词解析方式不统一，可能涉及 KRC/QRC、逐字歌词、翻译歌词和专有加密/编码。
7. 播放质量选择由曲目可用质量与当前 API source 对该平台声明的 quality 列表共同决定；播放 URL 获取会优先读取 URL 缓存，失败时可按匹配结果尝试其他音源，记录 retryedSource 防止重复尝试。Local source 的 musicUrl/lyric/pic 由用户自定义源提供。
8. 由此得到关键结论：洛雪自定义源脚本原生协议只负责补充平台的 URL（远程 source）或 local source 的 URL/歌词/封面；洛雪桌面端平台搜索/详情/歌单等功能由独立 SDK 代码实现。若 My-LiveHub 要求“可导入 JS 脚本实现任意音源搜索和播放”，它要兼容的对象不能简单等同于桌面端自定义源协议；必须另行确认目标脚本生态和脚本 API 格式，再明确选择“移植平台 SDK 模块”或“支持另一类 JS provider 脚本”，不能宣称这是原生 LX 自定义源 action。

### 10.8 官方自定义源运行时生命周期和请求语义（第二批审计）

核对源码：`src/main/modules/userApi/utils.ts`、`src/main/modules/userApi/main.ts`、`src/main/modules/userApi/renderer/preload.js`、`src/main/modules/userApi/rendererEvent/rendererEvent.ts`、`src/renderer/core/useApp/useInitUserApi.ts`。

1. 导入：读取脚本头部块注释元数据（name/description/author/homepage/version），对字段长度截断；deflate 后 base64 保存；按压缩内容防重复；生成 user_api ID。脚本内容单独缓存，元数据持久化；支持旧格式迁移、启用更新提示、删除。
2. 执行隔离：每次加载先关闭旧 BrowserWindow；创建隐藏 Electron BrowserWindow，`contextIsolation=true`、`nodeIntegration=false`、`nodeIntegrationInWorker=false`，禁用若干导航/新窗口/权限能力；但 `sandbox=false`，因此不能把它描述为完整 OS 级沙箱。
3. preload 在收到 initEnv 后通过 contextBridge 暴露 `globalThis.lx`，再注册全局 error/unhandledrejection 捕获，最后执行脚本。脚本须先注册 request handler，再发送 inited；初始化错误会通过专用错误 handler 报告。
4. 初始化只允许一次。宿主校验 source key、type、action、quality，并只保留白名单能力；renderer 收到状态后将支持的 action 映射为 `getMusicUrl/getLyric/getPic` 方法。切换 source 时旧 API 请求会因 source changed 被拒绝。
5. 请求由 requestKey 关联。renderer 主侧设置 20 秒超时；请求取消会 reject 对应 Promise 并清理队列和 timer。切换或关闭窗口时有资源清理。preload 的 `lx.request` 默认 60 秒，上限 60 秒，支持 method/headers/body/form/formData、当前代理设置及 abort closure；响应同时给 statusCode/statusMessage/headers/bytes/raw/body，body 尝试 JSON parse。
6. `lx.request` 把 response raw 转字符串后作为 body；因此对二进制数据的原始语义、编码、Buffer 表现需要重点验证，不能简单视为 Node needle 完整响应兼容。宿主提供 Buffer、zlib、crypto（AES/RSA_NO_PADDING/randomBytes/MD5）接口。
7. `lx.send` 支持 inited 和 updateAlert；updateAlert 每次运行仅允许一次，日志长度受限。脚本运行环境为桌面 Electron renderer，不暴露 Node integration，但并非 QuickJS 的等价全局环境。
8. 关闭 BrowserWindow 前清理 auth cache、storage、cache；卸载或切源时销毁旧窗口。代理变化会通过 proxyUpdate 事件更新 preload 代理配置。
9. 重要兼容边界：原生请求 action 的主侧超时为 20 秒，HTTP request timeout 最多 60 秒；取消链路跨 renderer/主进程；脚本环境包含真实 BrowserWindow/Web APIs 的一部分。QuickJS 适配不能只补齐 lx 对象名称，还必须对齐初始化时序、Promise/回调、事件和请求生命周期、错误报告、数据类型、取消语义。

### 10.9 官方源码审计结论及对目标方案的约束

- **原生 LX 自定义源协议已明确**：远程 source 的 `musicUrl`；local source 的 `musicUrl/lyric/pic`。协议通过 `inited` 声明 source 能力，通过 `request` 事件承载 action。
- **LX 桌面端内置 SDK 已明确**：搜索、跨源匹配、平台详情/歌单/榜单/热搜/评论、歌词/封面等主要是桌面端 renderer 模块，而非远程自定义源 action。
- **API source 与用户自定义源的关系已明确**：平台 SDK 可将 URL 获取委托给当前 API source；当 API source 选择 user_api 时，由用户脚本补充 URL 或 local metadata。平台搜索仍由内置 SDK 模块完成。
- **因此原方案必须保持两条能力边界**：兼容 LX 原生用户自定义源协议是一项；实现可导入 JS provider 来承担搜索/详情/歌词等完整音源能力是另一项。后者若仍是产品目标，必须选定真实脚本格式/生态并以真实脚本样本验证，不能把扩展 action 伪装成 LX 原生协议。
- **安全边界**：官方 BrowserWindow 不是完整 sandbox（sandbox=false）；My-LiveHub QuickJS 需要以更严格的权限、网络目标校验、DNS/重定向逐跳检查、执行资源配额实现安全目标，不应照搬官方较宽松行为。
- **兼容性边界**：需要覆盖全局对象/Polyfill、Buffer 二进制语义、zlib/crypto、HTTP response、请求代理、异步事件、Promise、取消、初始化时序、脚本生命周期。仅测试一个 HelloWorld 或只测 musicUrl 无法代表真实兼容。

### 10.10 阶段 0 审计状态更新

本轮完成了官方内置 Music SDK 搜索/跨源匹配/播放质量与 URL 调度、各平台模块能力，以及自定义源导入/执行/初始化/请求/卸载生命周期的第二批源码核对。审计仍需最后完成两类证据收敛：
- 对 My-LiveHub 现有 QuickJS host API、运行时限制、并发、网络安全、测试用例逐项建立带文件/符号/测试名称的差异矩阵；
- 收集并运行至少一组目标生态真实脚本（不只是官方最小示例），确认用户最终希望导入的脚本格式和兼容范围。

在这两项完成前，阶段 0 仍不得标记全部完成，也不得进入阶段 1 实现。


### 10.11 My-LiveHub QuickJS / host / dispatch 实现差异矩阵（第三批审计）

核对当前分支源码：`app/internal/music/lxruntime/{host_contract.go,runtime_quickjs.go,initialize.go,dispatch.go,http_request.go,source_manager.go}`、对应测试，以及 `app/internal/desktop/lx_script_runtime*.go`、`app/internal/music/{source_service.go,search_lx.go,lyrics_lx.go}`。

| 能力 | 官方 LX 行为 | My-LiveHub 当前实现 | 审计判断 |
|---|---|---|---|
| JS 引擎 | Electron Chromium/V8 renderer | QuickJS-NG CGo，单独 owner goroutine / OS thread | 引擎不同，必须按脚本 API 兼容而非引擎等价验收 |
| 内存/栈/执行时限 | 由 BrowserWindow/Chromium 管理，源码未见相同硬配额 | 默认 32 MiB、512 KiB、Eval 3 秒，可配置 | 已有基础限制；还需检查长生命周期累计分配与脚本隔离 |
| 生命周期 | 每个用户源独立 BrowserWindow，切源关闭旧窗口 | 每个 managed script 独立 QuickJS Runtime；SourceManager 固定脚本 | 有隔离上下文，但尚无导入/切换/删除 API |
| 初始化 | preload 注入 lx，执行脚本，request handler 注册后 inited 握手 | InstallLXHost + Eval，泵 timer/HTTP 请求直至 initialized | 有协议骨架；需补齐异常初始化状态与完整生命周期 |
| source/action/quality | 官方白名单过滤 | 同样使用 kw/kg/tx/wy/mg/local 和 action/quality 白名单 | 基本对齐；local 缺省 qualitys 处理需用官方样本回归 |
| request action | requestKey 主侧队列，20 秒超时/取消 | QuickJS handler Promise dispatch，context cancel；HTTP task 有取消队列 | 核心路径存在；尚需并发/取消竞态与多请求压测 |
| lx.request | Needle，60 秒上限，代理、method/headers/body/form/formData，回调含 raw/bytes/body | Go net/http，60 秒上限，8 MiB响应上限，JSON parse 或 string，支持表单和 multipart | API 形状接近；Needle 原始响应、编码、代理和重定向行为并不等价 |
| HTTP 安全 | 官方窗口权限有限，但源码未展示 SSRF 防护 | 当前只校验绝对 HTTP(S) URL；默认 net/http redirect 行为；未见 DNS 解析后 IP 校验 | **安全 blocker**：本地/私网/链路本地/云元数据地址、DNS rebinding、重定向逐跳检查尚无证据 |
| Buffer | Buffer.from / bufToString | host bootstrap 将 buffer 方法标为 unavailable | **兼容缺口** |
| zlib | inflate / deflate Promise | host bootstrap 将 zlib 方法标为 unavailable | **兼容缺口** |
| crypto | AES、RSA_NO_PADDING、randomBytes、MD5 | InstallLXHost 安装 native crypto；需对照模式、编码、padding、返回 Buffer 的一致性 | 部分实现，不能只看符号存在即判定兼容 |
| browser globals | Chromium Web APIs、globalThis、定时器等 | console、timers、queueMicrotask、部分 browser bootstrap | **部分模拟**；DOM/fetch/URL/Blob/编码等需按真实脚本依赖逐项验证 |
| 脚本导入 | 元数据解析、压缩存储、去重、列表/删除/更新提示 | SourceManager 三个 go:embed 脚本 | **未实现用户脚本导入管理** |
| 搜索 | LX desktop 内置 SDK 的平台 search 模块 | Go 原生五平台 provider | **与用户确认的 JS 搜索目标不一致**，但尚不能用自定义源原生 action 替代 |
| 歌词 | 平台 SDK 自行获取/解码；自定义源 local lyric 是另一机制 | Go 原生歌词（已核对 QQ/KG 等实现） | **未由导入 JS provider 统一承担** |
| URL 返回 | 原生 musicUrl 返回 HTTP(S) 字符串 | 返回 string，统一服务构造旧式 musicInfo | 基本 action 结果兼容；缺少结构化媒体请求上下文 |
| 播放主链 | HTMLAudio/Wails 主窗口桥 | `main.go` 生产入口通过 `AttachHTMLAudioPlayback` 使用资源感知引擎；默认测试构造仍保留旧 engine | 真实目标 WebView 实播待验收 |

**安全审计具体发现：** `executeLXHTTPRequest` 只解析并限制 URL scheme/host；没有在发起请求前解析 DNS 并拒绝 loopback/private/link-local/multicast/reserved 地址，也没有为每次 redirect 重新校验目标地址。Go client 的默认 redirect policy 可继续跟随跳转。仅靠 QuickJS 不暴露 OS API 无法防止脚本借 `lx.request` 访问本机或内网服务。这是用户脚本导入前必须解决的安全 blocker。

**运行时并发发现：** `Runtime` 以单一 command channel 和单一 QuickJS owner thread 执行；`dispatchMu` 保证同一 runtime 的 action dispatch 串行。SourceManager 另有全局 `dispatchMu`，因此当前所有脚本 action 实际串行，且脚本初始化/请求时的锁持有范围需要并发压测，不能承诺多源并发执行。

**测试覆盖已核实：** 当前已有 host 初始化/能力过滤、SCRIPT_MD5、HTTP method/body/form/multipart、超时、URL scheme、响应体大小上限、timeout clamp、truthy body 语义等测试。测试使用 lxquickjs && cgo build tag；生产 runtime 使用 lxproduction && cgo。尚未见完整第三方脚本兼容套件、SSRF/DNS/redirect 防护测试、并发隔离/压力测试、Windows Wails WebView 真实播放验收。测试存在不等于生产构建或全平台 CI 已通过。

### 10.12 阶段 0 审计交付判定

当前已形成三层源码证据：
1. 洛雪官方自定义源规范与桌面端 preload/IPC/导入生命周期；
2. 洛雪内置 Music SDK 搜索、跨源匹配、平台数据模型和播放调度；
3. My-LiveHub 当前 QuickJS/host/request/dispatch/Go provider/播放接入实现。

本轮审计已足以确定原方案中的关键误区与实现差距，但**不能诚实地把阶段 0 宣告为全部完成**：还缺少用户指定脚本生态的真实脚本样本和逐脚本执行验证，以及 Windows Wails WebView HTMLAudio PoC。前者决定“JS 搜索脚本”究竟兼容哪一种脚本格式；后者决定最终播放链路可行性。二者均不能由静态源码审计代替。

因此当前审计结论为：**官方源码静态审计主干已完成；目标脚本生态兼容验证和播放 PoC 属于审计收尾的实测 gate，阶段 0 暂不关闭。** 下一步不扩展功能开发，而是先从仓库现有真实脚本/用户明确指定的脚本生态取得可复现样本，建立 fixture 和逐 API 兼容测试；同时修订主方案中的安全 blocker 和 HTMLAudio 验收门槛。


### 10.13 真实脚本样本盘点与 SSRF 第一轮修复

本轮从当前分支实际嵌入的脚本目录读取并分析以下样本（不是根据文件名推测）：

| 样本 | 文件 | 观察到的实际依赖/行为 | 当前兼容结论 |
|---|---|---|---|
| 星澜聚合音源 v4.0.2 | `app/internal/music/lxruntime/testdata/sources/xinglan.js` | 约 71.9 KB / 1651 行；使用 `globalThis.lx`、`currentScriptInfo`、`lx.request`、`utils.crypto.md5/aesEncrypt`、`utils.buffer.bufToString`；含多平台 musicUrl 后端、请求头、Cookie 配置和音质映射 | 有价值的真实复杂脚本样本；当前 action handler 只分发 musicUrl，必须验证 Buffer/crypto/响应体和真实请求链 |
| HelloWorld 音源 | `app/internal/music/lxruntime/testdata/sources/helloworld.js` | 约 21.6 KB / 557 行；使用 `lx.request`、动态 API 配置、headers/body、Buffer/base64 token 生成；声明远程 musicUrl 和 local musicUrl/lyric/pic | 可验证请求参数、请求头、Buffer/base64、动态配置及 local action；不代表搜索 action |
| 幻音音源 | `app/internal/music/lxruntime/testdata/sources/huanyin.js` | 约 8.3 KB / 353 行；实现 musicUrl，并额外注册 mg search handler；使用 URL、Map、Promise、定时器和 lx.request | 暴露协议边界问题：脚本 handler 有 search 分支且 inited 声明 mg search，但官方桌面 preload 的远程 action 白名单会过滤 search；不能据此宣称它是洛雪原生 search action |

**样本审计结论：**
- 当前三个嵌入脚本均不能自动代表“完整洛雪 JS 搜索生态”。星澜和 HelloWorld 的真实 handler 主体是 musicUrl；幻音的 search 是脚本自定义行为，与官方远程自定义源 action 白名单不一致。
- 真实兼容测试必须把原生桌面自定义源协议测试与“完整 JS provider 搜索”测试分开。后者必须明确目标生态/脚本约定，并对真实脚本执行，不得把自定义 action 伪装成官方原生能力。
- 星澜脚本直接依赖 AES/MD5/Buffer，并从 HTTP response body 读取 JSON 或 Buffer-like 数据；当前测试集必须覆盖实际加密模式、编码、二进制转换、请求头、请求取消及多请求并发。
- 现有脚本配置含外部 API endpoint，样本文件中出现明文访问凭据/卡密的风险；这些样本只用于兼容性分析，不应在日志、测试报告或新增文档中复制密钥。后续应对仓库中的敏感值做单独清理审计。

**本轮已落地的 SSRF 代码修复：**
- 新增 `app/internal/music/lxruntime/ssrf.go`，对脚本 HTTP 请求执行目标主机校验。
- 默认 `http.Transport` 使用独立安全 transport；禁用环境代理绕过安全策略，解析 DNS 后逐个校验地址，并将连接固定到已校验 IP，避免 DNS rebinding 的校验/连接竞态。
- 拒绝 URL userinfo、非 HTTP(S)、loopback、私网、link-local、multicast、unspecified、共享地址、基准测试网段及已知特殊用途 IPv4/IPv6 地址。
- 每次 HTTP redirect 都重新验证目标；默认最多允许 5 跳。自定义 RoundTripper 若无法保证 DNS pinning，在生产策略下对域名请求 fail closed。
- `executeLXHTTPRequest` 的生产入口始终使用严格策略；只有测试专用的 `executeLXHTTPRequestWithPolicy(..., true)` 可以访问 httptest loopback，避免为测试放宽生产边界。
- 新增 `ssrf_test.go` 覆盖公有/私有/保留地址分类、私网 IP 拒绝、URL credentials 与 scheme 校验；原有 HTTP 桥接测试改为显式使用测试专用本地策略。

**当前证据状态与限制：**
- SSRF 修复代码已提交到目标分支，但本轮尚未执行 Go 测试、生产 build 或 CI；因此只能称为“代码已落地、待测试验证”，不能称为安全 blocker 已完全关闭。
- 仍需补充 DNS 解析混合结果、DNS rebinding、重定向跳转到私网、IPv4-mapped IPv6、代理绕过、自定义 transport、取消与超时的测试。
- 尚未取得并运行用户明确指定的目标生态外部真实脚本集合；当前三份是仓库已嵌入样本。需先以这些样本建立 baseline，再扩充至少一份来自目标生态的独立真实脚本，避免只验证项目自带样本。
- 搜索、播放 URL、歌词、请求头、AES/RSA/MD5、Buffer/zlib、二进制响应、请求取消和质量回退应分别形成可重复 fixture 测试；真实线上 endpoint 采样需记录时间、状态码、响应类型及脱敏后的请求元数据，不保存令牌、Cookie 或签名 URL。

阶段 0 继续保持进行中；本轮只落地网络安全第一轮实现并完善真实样本审计，不进入阶段 1 功能开发。


### 10.14 真实脚本兼容测试集第一版与验证状态

已新增 `app/internal/music/lxruntime/real_script_compat_test.go`，直接通过 `go:embed` 加载仓库现有三份完整脚本，不再以手写的最小 JS 片段替代真实样本。当前第一版测试覆盖：
- 三份真实脚本能否在 QuickJS 中完成桌面 LX host 初始化握手；
- source/action/quality 能力经过 host 白名单协商后的实际结果；
- 幻音脚本虽然实现了 `search` handler 并声明 mg search，但 host 只接受官方远程 `musicUrl` action，测试明确锁定此协议边界。

新增 SSRF 测试覆盖：
- 公网、私网、loopback、link-local、共享地址、benchmark、文档保留地址及部分 IPv6 特殊用途地址分类；
- URL credentials、非 HTTP(S) scheme、私网 IP 目标拒绝；
- 通过 fake RoundTripper 验证重定向到 loopback 会在发起第二跳前被拒绝；
- 本地 httptest 仅通过显式测试 helper 放行，不改变生产入口的默认拒绝策略。

**当前仍未覆盖、不得宣称通过的兼容项：**
1. 星澜真实 musicUrl 的端到端 URL 解析（需对脚本实际访问的多个 endpoint 做可控 mock/录制回放，不能调用未知第三方接口冒充可重复测试）。
2. HelloWorld 的多后端质量回退、动态请求 headers/body 和 URL 失效回退。
3. 歌词/封面真实返回格式；三份当前脚本没有共同的远程 lyric action，local lyric/pic 需单独测试。
4. 最终媒体 URL 所需的 Referer/Cookie/User-Agent、Range、重定向及有效期；必须与脚本 API 请求头分开采样。
5. AES 模式/填充/编码、MD5、RSA、Buffer 二进制往返、zlib 压缩解压与官方 Electron preload 行为的一致性。
6. DNS rebinding 的可控 resolver 测试、DNS 多地址混合结果拒绝、代理绕过、请求取消、并发隔离和超时竞态。
7. 用户明确指定的目标生态外部真实脚本。当前三份是仓库现有脚本，尚不能代表外部脚本生态的覆盖率。

**提交与验证记录：**
- SSRF 实现及测试、真实脚本初始化测试和本节主方案更新均已提交至 `feat/lx-js-runtime-quickjs-poc`。
- 最新相关代码提交：`6c22c85288537cc8739e647dd44033d2d7d5503c`（包含真实脚本初始化测试、SSRF 地址策略及私网/重定向/NAT64 等测试用例）。
- 本轮查询未返回该提交的 GitHub status checks 或 workflow runs；且本执行环境没有直接检出仓库运行 `go test` 的能力。因此所有新增测试状态均为**已编写、待 CI/本地实际执行**，不能标记通过。
- 目前 SSRF blocker 的状态应记录为“严格网络策略代码已落地，自动化测试待执行和补齐”，不是“安全审计完成”。

阶段 0 仍保持进行中。下一轮优先完成 DNS/redirect 安全测试和真实脚本 API 依赖验证，再取得目标生态独立脚本；不提前进入阶段 1，也不把仓库自带脚本误当成用户最终指定的全部目标。


**10.14 补充记录（后续安全收敛）：** 进一步将 IPv6 目标限制在 `2000::/3` 全球单播空间，并拒绝 IPv4-compatible、NAT64、6to4、Teredo/协议分配及文档保留地址；自定义 HTTP RoundTripper 若无法进行 DNS pinning，在生产策略下对域名请求直接拒绝。新增测试覆盖 loopback/私网/云元数据地址、重定向至 loopback、IPv4-compatible 与 NAT64 地址。最新安全代码提交为 `6c22c85288537cc8739e647dd44033d2d7d5503c`。仍需 CI 实际执行及可控 DNS rebinding/mixed-answer 测试，不能将安全验证标记为完成。


### 10.15 外部目标生态真实脚本采样（pdone/lx-music-source，固定版本）

已从公开目标生态仓库取得并逐段审阅外部脚本源码（仅用于兼容性审计；未将第三方完整脚本复制进本仓库）：

- 上游仓库：`pdone/lx-music-source`
- 固定审计 commit：`07dca9589e3734b140ec4ef396ee29dfe63687a2`
- 仓库 README：`https://github.com/pdone/lx-music-source/tree/07dca9589e3734b140ec4ef396ee29dfe63687a2`
- 样本 A：`feichangdao/latest.js`，blob `c1724ef140e03bdca336bb92c397c4d0a5a26199`
- 样本 B：`xinghai/latest.js`，blob `3eb0ce8c233ba5e1b2f0f1b4d70856e6accb3be5`
- 对照样本：`xinglan/4.0.2.js`，blob `f12581f4d2b05b6585480fd3436595836f91040a`

**样本 A：非常刀（FeiChangDao）**
- 约 17.1 KB / 443 行。
- request handler 明确分支 `musicUrl`、`search`、`lyric`；初始化声明 wy/kw/mg/qs 等 source 的 search，qs 还声明 lyric。
- 搜索支持网易云、酷我、咪咕、汽水等不同后端；歌曲结果映射为 source/id/name/singer/album/interval/meta 结构。
- 歌词从汽水 API 获取 KRC 并转换成 LRC。
- URL 解析使用 `Promise.any` 并发竞速前三个后端，失败后顺序回退；有缓存、质量映射和后端降级。
- 请求示例使用自定义 Referer 和 Accept headers。
- 这份脚本真实证明目标生态中确实存在“search/lyric 扩展 action”脚本，但这些不是洛雪桌面官方远程自定义源原生 action。

**样本 B：星海（XingHai）**
- 约 20.2 KB / 538 行。
- handler 支持 `musicUrl`、`lyric`、`pic`；初始化为多个远程平台声明这三种 action。
- 使用 `lx.utils.buffer.from/bufToString` 处理 base64/请求数据；使用请求 headers、设备标识/token、更新提示和缓存。
- 启动流程除 inited 外还会异步发起 IP 查询、版本检查等 HTTP 请求；因此真实脚本初始化测试必须能隔离/回放初始化网络任务，不能让 CI 任意访问第三方服务。
- 脚本把远程平台 lyric/pic 声明为 action，与官方桌面 preload 只允许远程 musicUrl、local 才允许 lyric/pic 的白名单存在明确差异。

**生态兼容结论更新：**
1. 用户提出的“搜索、播放 URL、歌词由真实 JS 音源脚本承担”在第三方脚本生态中有真实样本依据，不是臆想需求。
2. 但目标脚本集合包含两种协议形态：严格 LX 官方 desktop custom-source 协议，以及社区扩展了 search/lyric/pic 的扩展脚本。My-LiveHub 必须明确保留原生协议兼容层，同时单独定义并测试扩展 action 兼容能力；不能把社区扩展 action 说成洛雪原生能力。
3. 搜索结果需要由 Go 适配层映射成统一 Track；歌词结果需要兼容 string/object/null、LRC/KRC 等形态；播放 URL 需要连同媒体请求 headers/有效期建模。
4. 测试不能只测 action 名称。至少应覆盖：FeichangDao 的搜索/歌词/Promise.any URL 回退/Referer；XingHai 的远程 lyric/pic/Buffer/base64/token/初始化网络请求；XingLan 的 AES/MD5/Buffer/多后端质量映射。
5. 这些脚本来自不受 My-LiveHub 控制的第三方仓库，README 明确警告仓库曾下架含恶意逻辑的 LX 脚本。不得批量自动执行订阅仓库所有脚本；必须固定 commit/blob、人工审查样本、在 QuickJS 无 OS 能力的隔离环境执行，并对 `lx.request` 强制 SSRF/资源配额。当前不把被警告下架的 `lx/latest.js` 作为兼容 fixture。
6. 第三方样本未明确授权再分发完整源码，因此本项目只记录固定版本、blob SHA、来源链接和测试观察，不把完整第三方脚本复制进仓库。后续若需要离线 CI fixture，应先确认许可或取得用户提供的可测试副本，再计算并固定 SHA-256。

**目标脚本采样已完成第一轮；真实执行仍未完成。** 当前测试文件已对仓库自带三份脚本做初始化握手验证，但尚未对外部 FeichangDao/XingHai 执行 action。下一步需要在可注入、可录制回放的 HTTP Host 层完成测试隔离，再逐项执行真实外部脚本；不可直接放开 CI 出网。


### 10.16 脚本网络请求资源配额补强

在 SSRF 地址策略之外，进一步为每个脚本发起的 `lx.request` 增加宿主侧资源边界：
- URL 最大 8 KiB；
- 请求 body 最大 2 MiB；
- 请求 header 最多 100 项，key/value 总计最多 64 KiB，并拒绝 CR/LF header injection；
- 响应 body 最大 8 MiB；
- 全局同时执行的 LX HTTP 请求最多 8 个，等待 semaphore 时可响应 context cancel；先取得网络槽位再构造请求 body，避免大量并发任务先分配大 body；
- 默认 HTTP 超时/最大超时仍为 60 秒；请求任务总数仍由 dispatch/init 的上限约束。

已新增 `TestLXRequestResourceLimits` 覆盖超大 URL、超大 body、header injection 和 header 数量限制。当前测试代码仍待 CI/本地实际运行。上述配额是 My-LiveHub 的安全策略，不宣称与洛雪 Needle 对任意超大请求完全等价；真实脚本兼容性若受限，应以明确错误和可配置的安全上限处理，不得移除安全边界。


**10.16 补充记录：** 生产 transport 还会清除调用方可能预设的 `DialTLS` / `DialTLSContext`，强制 HTTPS 握手走已校验并 pin IP 的 `DialContext` 路径，防止自定义 TLS dialer 绕过 SSRF 校验。对应代码提交：`8f7a521b787e29293f3dbad1f3e1eb88572f0fed`。CI status/workflow 查询仍为空，全部测试继续标记为待实际运行。

### 10.17 本轮完整源码审计与修复记录（2026-11-23）

本节覆盖本轮对主方案、当前 feature 分支、洛雪官方源码和目标生态脚本的源码审计。它是此前 10.13–10.16 记录的最新状态；历史记录中的“待 CI”状态以本节和最新 workflow 结果为准。

#### A. 协议边界与真实脚本审计

**洛雪官方协议（固定源码版本 323e4bac505da428adae8db6b58039fb2b36b86d）**

- 官方 EVENT_NAMES 为 request、inited、updateAlert；lx.request(url, options, callback) 使用 Needle 风格请求，支持 method、headers、body/form/formData、timeout、response raw/body 和 abort/cancel。
- 远程自定义源原生 action 是 musicUrl；local source 还允许 lyric、pic。官方内置 musicSdk 的搜索/详情 API 与自定义源 action 是不同层级。
- 因此 search、detail 不是官方远程自定义源原生 action。社区脚本的 search、远程 lyric、pic 必须视为扩展能力，不得写入官方原生 action 白名单或称为洛雪原生协议。
- musicUrl 官方结果是 HTTP(S) URL 字符串；部分社区脚本返回包含 url/lyric/cover 的对象，不能假定所有生态脚本都满足原生返回类型。

**真实目标生态样本（静态源码审阅）**

| 样本 | 来源与固定版本 | 已核对行为 | 当前验证等级 |
|---|---|---|---|
| Xinglan 4.0.2 | pdone/lx-music-source commit 07dca9589e3734b140ec4ef396ee29dfe63687a2，blob f12581f4d2b05b6585480fd3436595836f91040a | musicUrl、咪咕 URL 路径质量回退、HEAD 探测、请求 headers、AES/MD5/Buffer 和多后端回退 | 仓库固定快照 + mock 测试 |
| FeichangDao | 同一 commit，blob c1724ef140e03bdca336bb92c397c4d0a5a26199 | 社区 search/lyric 扩展、KRC→LRC、Promise.any、缓存、质量回退、Referer/Accept headers | 仅静态审阅，未在本仓库执行 |
| XingHai | 同一 commit，blob 3eb0ce8c233ba5e1b2f0f1b4d70856e6accb3be5 | musicUrl/lyric/pic 扩展、初始化 IP/版本请求、Buffer/base64、token/headers | 仅静态审阅，未在本仓库执行 |
| HelloWorld | 仓库固定测试快照 | 请求/响应、错误、初始化和播放 URL 测试 | mock 测试 |
| Huanyin | 本项目自制兼容性 fixture，不是外部真实音源 | 用于搜索/协议测试的模拟 action | 仅测试 fixture，不得作为生产音源 |
| lx.js | 上游曾下架并被 README 警告含恶意逻辑的脚本快照 | 仅用于隔离环境中的协议/签名测试 | 不作为生产音源，不纳入常规真实音源池 |

上游样本仓库没有可确认的再分发授权。本项目不把 FeichangDao/XingHai 的完整第三方源码复制到生产二进制；后续如需离线 fixture，先取得许可或用户提供的副本并固定 SHA-256。不得自动批量执行订阅仓库中的任意脚本。

#### B. QuickJS 宿主 API 兼容性审计

- lx.request：Go 请求队列、回调、response/raw/body、超时、取消；HTTP 响应 header 统一为小写 key，普通 header value 为字符串，set-cookie 保留数组；statusMessage 使用 reason phrase，raw 以 Buffer 语义交给 JS。
- lx.utils.crypto：MD5、AES、RSA、randomBytes。当前 AES 实现覆盖脚本样本使用的 CBC/ECB 路径；不宣称等同 Node crypto 全部算法/参数。
- lx.utils.buffer / Buffer：覆盖当前样本使用的 from、alloc、concat、toString、byteLength、isBuffer 等常见操作；Buffer 有容量上限，不是完整 Node Buffer API。
- lx.utils.zlib：deflate/inflate Promise 语义，输入/输出受限；原生桥接生成的字节结果统一包装成 Node-compatible Buffer，而不是裸 Uint8Array。
- 初始化/事件：核对 currentScriptInfo、lx.version、lx.env、lx.on、lx.send、inited 和 updateAlert 的实际调用链。
- 定时器/Promise：QuickJS owner-thread 执行、事件循环泵送和请求回调生命周期受宿主控制。
- 明确差异：不提供完整 Chromium DOM、fetch、Node process/require、文件系统模块或完整 Node Buffer/crypto；代理请求被安全策略禁用；社区扩展 action 还没有独立的生产调度接口。

#### C. 本轮已落地的安全与生命周期修复

1. 全局 HTTP semaphore 在 DNS 预检之前获取，避免并发请求在限流前放大 DNS 查询。
2. header 名称执行 HTTP token 校验；拒绝控制字符、hop-by-hop/framing headers（Host、Content-Length、Connection、Transfer-Encoding 等）。
3. 重定向逐跳校验，限制跳数，拒绝 URL credentials，并拒绝 HTTPS→HTTP downgrade。
4. 生产路径拒绝自定义 RoundTripper，避免自定义 transport 忽略 req.URL 后自行连接任意地址；代理配置被清除，TLS 专用 dialer 被清除，连接必须经过已校验的 DialContext。
5. DNS 所有 A/AAAA 结果整体校验；混合公网/私网答案整体拒绝；连接时再次解析并只拨号校验过的 IP。增加可注入 resolver 的确定性 DNS rebinding 测试。
6. lxHTTPTasks 在 abort 时立即移除 active 项；取消时等待所有请求 goroutine 退出，避免脚本关闭后遗留 HTTP goroutine。
7. SourceManager 默认 HTTP timeout 与 LX request 60 秒上限一致；Close 先取消 manager context，再等待 dispatch lock，避免初始化/网络请求阻塞关闭。
8. SourceManager 的播放 URL 结果先做公网地址校验，并在 DNS 校验期间占用全局网络 semaphore。这仍不是媒体连接 pinning：WebView HTMLAudio 直连时 DNS 可能在校验后变化；最终必须通过 Go 受控媒体代理完成连接 pinning、redirect 和媒体 headers 策略。
9. 测试私网放行仅由包内 test helper 设置，生产 Runtime/SourceManager 不启用该策略。
10. 生产 SourceManager 不再把 testdata/sources 的测试快照（尤其项目自制 Huanyin）当成内置生产音源。新增 NewSourceManagerWithScripts 接收脚本管理层显式提供的脚本；默认 NewSourceManager 不捆绑社区/测试脚本。导入、持久化、授权和刷新尚未接入该入口。

#### D. 测试与 CI 记录

- 必需 CI 改为 QuickJS 专项音乐检查：构建 QuickJS-NG CGo/native smoke；运行 go test -tags lxquickjs ./internal/music/lxruntime ./internal/music、go vet；运行 go test -tags lxproduction ./internal/music/lxruntime ./internal/music。
- 已移除必需 CI 中的实时第三方 /rconfig 握手；LX_RCONFIG_LIVE 不再作为 CI 必过条件。实时握手保留为可选诊断。之前 live handshake 连续返回 HTTP status 0，没有取得 HTTP 响应，因此该外部握手当前标记为外部阻塞，不把它误判为签名算法失败。
- 已移除该 workflow 的 Node/Wails frontend build、完整桌面程序构建和 installer/portable ZIP 步骤；feature 分支 push 只触发 QuickJS 专项 workflow。PR #45 已关闭且未合并。
- c643fef2460bef50f4e2d8d4e5 对应 QuickJS workflow run 45115196974：QuickJS native build、lxquickjs music tests、lxproduction music tests均通过。
- 后续 source-manager 不再捆绑测试脚本、播放 URL 安全校验和本节文档更新属于后续提交；必须以本节对应最新 commit 的 workflow 结果作为最终 CI 状态，不能沿用 c643fef 的成功结果替代最新代码验收。

#### E. 完整验收状态矩阵

| 审计/验收项 | 状态 | 证据与未完成项 |
|---|---|---|
| 洛雪官方原生协议边界 | 已完成（源码审计） | 固定官方 commit；已区分 native action 与社区扩展 |
| Xinglan 真实快照播放 URL/质量回退/请求头 | 测试通过（mock） | QuickJS 专项 CI；不是第三方在线服务实测 |
| HelloWorld 固定快照初始化/请求回调 | 测试通过（mock） | QuickJS 专项 CI |
| Huanyin 搜索 action | 测试 fixture 通过 | 本项目自制模拟样本，不计入真实生态脚本验收 |
| FeichangDao search/lyric/Promise.any | 待验证 | 仅静态审阅；需在可控 mock 下执行固定脚本 |
| XingHai lyric/pic/Buffer/init 网络 | 待验证 | 仅静态审阅；需在可控 mock 下执行固定脚本 |
| 歌曲详情 | 待实现/待验证 | 官方原生协议没有 detail action；社区扩展调度尚未实现 |
| 搜索统一调度 | 未完成 | 当前 Search 仍由 Go LXSearchClient providers 执行，不是 QuickJS 社区脚本 action |
| 歌词/封面统一调度 | 未完成 | 当前 GetLyrics 仍走旧播放引擎；QuickJS lyric/pic 扩展未接入 |
| QuickJS 原生 Buffer/crypto/zlib | 测试通过（当前覆盖范围） | 本地 QuickJS 宿主测试；不等于完整 Node API |
| 初始化/请求取消/并发限制 | 测试通过（当前覆盖范围） | mock/生命周期测试；真实外部脚本初始化仍待执行 |
| SSRF/DNS rebinding/redirect/proxy/header | 测试通过（当前覆盖范围） | 可控 resolver 与 mock；媒体 URL 的 WebView 最终连接尚未 pin |
| SourceManager 关闭与资源释放 | 测试通过（当前覆盖范围） | cancellation/waitgroup 测试；持续播放 soak 未做 |
| 用户脚本导入/持久化/授权/刷新 | 未完成 | NewSourceManagerWithScripts 已提供入口，尚未接入现有 UI/config/store |
| QuickJS 运行时接入实际播放链路 | 未完成 | PlayTrackNow 仍走旧 LXPlaybackEngine.Load，不是 QuickJS 解析结果 |
| HTMLAudio 最终播放 | 已接入，实播待验证 | 应用级单例 Audio 与 Wails 命令/事件链已接通；目标设备真实来源可听播放待验收 |
| 媒体 headers/Range/redirect 代理 | 未完成 | 目前只处理脚本 API 请求，不支持 HTMLAudio 受控媒体代理 |
| 内存目标/2 小时稳定性 | 待验证 | 尚无实际 WebView/主进程增量与 soak 数据 |
| 可选 live /rconfig | 外部阻塞 | 之前三次 HTTP status 0，没有 HTTP 响应；required CI 已改为 deterministic mock |

**历史阶段结论（D 开始前）：** QuickJS 运行时与 Go Host API 的兼容性测试通过，但当时 HTMLAudio/媒体代理未完成。当前 D 已接通生产 HTMLAudio 桥并通过自动化 CI；音乐模块整体仍不能验收完成，真实 WebView 播放、受控媒体代理、歌词时钟和长时间稳定性仍待后续阶段。不得把 D 的 CI 通过写成整个音乐模块完成。


### 10.18 复核后补充：导入元数据一致性与请求体预分配上限（2026-11-23）

在最新分支 HEAD 811d46faa30754f4d422c0a6440fbab29e3d6e4b 上继续复核后发现两处边界问题，并已补充修复与回归测试：

- **脚本元数据与可执行源码不一致**：NewSourceManagerWithScripts 原先允许 ScriptInfo.RawScript 与实际 Source 不同。宿主 SCRIPT_MD5/currentScriptInfo 会依据 RawScript 生成，而 QuickJS 执行 Source，造成脚本身份/摘要不一致。现在导入时拒绝不匹配的 RawScript，并始终将元数据 RawScript 绑定为实际执行源码。测试：TestProductionSourceManagerRejectsMismatchedRawScriptMetadata。
- **请求体限制晚于 multipart 分配**：原先 FormData 在完整写入 bytes.Buffer 后才进行统一大小检查，超大输入会先消耗无界临时内存。现在 multipart 写入使用限额 buffer；JSON 原始 body 在解码前检查大小；URL-encoded 表单字段在编码前进行保守尺寸预检并在编码后再次检查。测试覆盖超大 multipart 和 JSON body。

状态：代码和回归测试已写入待提交变更；尚未由 CI 验证。该修复不改变 LX 协议或音乐模块架构，只收紧导入与请求资源边界。


### 10.19 第二轮安全复核：TLS handler 绕过与响应桥接内存放大（2026-11-23）

继续逐行检查 HTTP transport 和 QuickJS response bridge 后补充三项修复：

- 安全 HTTP transport 之前只在自定义 transport 指针上拒绝非空 TLSNextProto；若调用方把带自定义 TLS handler 的 transport 安装为 http.DefaultTransport，旧条件会漏检。现在严格策略不论 transport 来源都拒绝自定义 TLS protocol handlers，并新增覆盖 DefaultTransport 被替换的回归测试。
- lx.request 响应 raw 字节之前转换成 JSON 整数数组传给 QuickJS，8 MiB 响应可能膨胀为数十 MiB JSON，造成不必要的 QuickJS heap 压力。现在通过 JSON base64 字符串传输，并在 JS 侧用 Buffer.from(..., 'base64') 还原 Node-compatible Buffer；既有 binary raw octets 测试继续验证字节完全一致。
- URL-encoded Form 参数增加累计保守尺寸预检，避免大量小字段在最终 Encode 后才发现超限。

验证记录：上一轮提交 eb0635925546bb1545d1420bab4d75102db12078 的 QuickJS 专项 CI Run 45119572262 已全部成功（Windows x64 QuickJS native build、lxquickjs tests/vet、lxproduction tests）。本节新增修复尚未提交/验证。


### 10.21 最新专项 CI 结果与运行时可用性状态修正（2026-11-23）

- 修正 10.20 的 TLS 策略后，提交 73c2296438dce0211bf4e0de29458257cfebb3c1 对应 QuickJS-NG Windows x64 专项 CI Run 45120352557 **全部成功**：QuickJS native C API smoke、lxquickjs runtime/music tests、go vet、lxproduction runtime/music tests 均通过。
- 继续审查生产集成时发现 MusicService.GetSources() 仅以 scriptRuntime 非 nil 标记 Resolve 能力；但生产构造函数返回非 nil runtime，即使 SourceManager 为空也会错误报告可解析。新增 lxScriptRuntime.Available()、SourceManager.HasScripts()，将来源能力状态改为真实已导入脚本数量，并增加 desktop/source-manager 测试。该修复不代表用户脚本导入管理已完成；当前生产 SourceManager 仍未接入持久化导入数据。
- 上述可用性修复目前是待提交变更，需在下一次 QuickJS 专项 CI 通过后才能标记测试通过。

当前 HEAD（本节开始时）：73c2296438dce0211bf4e0de29458257cfebb3c1。该提交的专项 CI 全绿；本节追加的可用性修复尚待提交与 CI。


### 10.22 音乐服务生产集成的定向验证门（2026-11-23）

- 新增生产 runtime 可用性测试，确认 lxproduction 构建下没有导入脚本时，runtime 在 Start 前后均不得报告可解析能力。
- QuickJS 专项 workflow 增加仅针对音乐 runtime 集成的 desktop package 测试，运行两个定向用例：MusicService 来源能力状态，以及 production LX runtime 空脚本状态。该步骤不是 Desktop validation workflow，不执行前端全量构建、安装包、portable ZIP 或 release。
- 该步骤是为了验证此前只运行 lxruntime/music package tests 未覆盖的 desktop 接口变更。待最新 workflow 通过后记录结果。


### 10.23 定向 desktop 音乐集成步骤的 shell 引号修复（2026-11-23）

提交 28e1005cbb5d9fcd3bec5461c6318cb7fefd125a 的 QuickJS CI 中，新增的 desktop music integration step 在 bash 命令中缺少 -run 正则的闭合单引号，导致：
line 3: unexpected EOF while looking for matching single quote
这属于 workflow 命令转义错误，不是 Go 编译或测试失败。已移除不必要的行尾锚点并补齐闭合引号。该提交的 lxquickjs、go vet、lxproduction music/runtime 测试均通过；desktop 定向测试因 shell 语法未实际启动，修正后重新执行。


### 10.24 本轮源码审计状态汇总与验收边界（2026-11-23）

本轮审计基线为分支 feat/lx-js-runtime-quickjs-poc。最新代码提交 b4fafb3a6c04fc61172a6321323f450a65ea983f 的 QuickJS-NG Windows x64 CI Run 45121422401 **success**。该 workflow 实际通过：
1. QuickJS-NG native C API smoke test；
2. lxquickjs runtime/music 全量确定性测试；
3. lxquickjs go vet；
4. lxproduction runtime/music 测试；
5. desktop music runtime integration 定向测试（仅运行 MusicService 来源能力和 production LX runtime 可用性两个测试，不运行 Desktop validation、installer、portable ZIP 或 release）。

#### 10.24.1 审计状态矩阵


> 本矩阵记录的是 10.24 审计时的历史状态；生产脚本导入与 B 阶段 action 接线随后分别在 A 阶段及 10.26/10.27 记录中完成。当前状态以第 4、7 节和 10.27 为准。

| 审计项 | 状态 | 证据/边界 |
|---|---|---|
| 唯一主方案与架构约束 | 已完成审查 | 本文档为唯一依据；未更换 QuickJS-NG + Go 音乐服务 + HTMLAudio 目标架构 |
| 洛雪原生事件/action 协议 | 已完成源码核对 | request/inited/updateAlert、原生 musicUrl/lyric/pic 等能力与社区扩展 action 分开建模 |
| QuickJS 宿主 API | 部分完成 | 已核对初始化、事件、HTTP request、取消、crypto、Buffer、zlib 的实现及对应确定性测试；尚未覆盖全部外部脚本真实调用形态 |
| HelloWorld/HuanYin/XingLan 已有 fixture | 测试通过（当前 fixture 覆盖范围内） | mock 请求、初始化、协议动作、Buffer/crypto/zlib 等测试由 QuickJS 专项 CI 运行 |
| FeiChangDao/XingHai 等外部真实脚本 | 外部阻塞 | 上游 pdone/lx-music-source 固定审计提交 07dca9589e3734b140ec4ef396ee29dfe63687a2 未提供 LICENSE；不能擅自将完整第三方脚本复制进仓库作为离线 fixture。需要用户提供授权副本或明确可再分发许可 |
| 搜索/详情生产集成 | 待实现/待验证 | 现有生产 Search 仍走 Go LXSearchClient；社区 search/detail 扩展 action 尚未接入生产脚本导入和应用搜索主链路 |
| 播放 URL/音质回退 | 部分完成 | URL 解析 facade 和 SourceManager 具备测试；但生产 SourceManager 尚未装载持久化导入脚本，播放队列实际加载路径仍未完成脚本 URL 解析整合 |
| 歌词/封面生产集成 | 部分完成 | 现有 Go 原生歌词服务可测；社区 lyric/pic 扩展尚未完整接入统一生产服务 |
| 请求头/加密/Buffer/zlib | 测试通过（当前 fixture/宿主 API 范围内） | 已有 header 验证、AES/RSA/MD5/randomBytes、Buffer encoding/raw bytes、zlib 压缩解压测试 |
| 初始化/请求取消 | 测试通过（当前测试覆盖范围内） | 初始化握手、request callback、abort、timeout、重复完成等确定性测试 |
| SSRF/DNS rebinding | 测试通过（已实现策略范围内） | DNS 答案校验、连接时重新解析并按已验证 IP 拨号、非标准数字 IPv4 拒绝、混合公私地址拒绝 |
| 重定向/代理/TLS transport | 测试通过（已实现策略范围内） | 禁用代理与 CookieJar、清除 TLSNextProto/ForceAttemptHTTP2、自定义 RoundTripper 拒绝、逐跳重定向验证、降级与跨源敏感头限制 |
| 请求资源限制 | 测试通过（已实现策略范围内） | URL/body/header/response/并发/超时限制；multipart 使用限额写入；JSON/form 预检 |
| 脚本隔离/并发/资源释放 | 部分完成 | 每脚本独立 QuickJS runtime、堆栈/内存限制、请求并发预算和 Close 等已有实现与测试；真实多脚本导入生命周期及长时间 soak 尚未验收 |
| HTMLAudio/WebView 实际播放 | 桥接已接通，目标设备待验证 | 生产主窗口使用 HTMLAudio；媒体 Range/headers/受控 redirect/seek 和真实设备播放仍未完成 |
| 两小时内存稳定性 | 待设备验证 | CI 单测不能替代 Windows WebView 连续播放与 renderer/主进程内存 soak |

#### 10.24.2 本轮新增修复

- 脚本元数据 RawScript 必须与实际 QuickJS 执行源码逐字节一致，拒绝不匹配导入。
- 请求 JSON/multipart/form 请求体在编码/分配阶段实施上限，避免先分配超大 body 再拒绝。
- TLS transport 不再保留调用方 TLSNextProto/HTTP2 handler，统一走受控 DialContext；同时修正 Windows Go 默认 transport 预置 HTTP/2 handler 导致的误拒绝。
- HTTP response raw bytes 改为 base64 传输到 QuickJS，再由 Buffer.from(..., 'base64') 还原，避免整数数组导致响应 JSON 内存放大。
- MusicService 来源能力状态基于 SourceManager 是否实际含有导入脚本，不再因 runtime 对象非 nil 就错误报告 Resolve 可用。
- QuickJS CI 增加仅限音乐 runtime 的 desktop 集成测试，不触发 Desktop validation 或桌面发布打包。

#### 10.24.3 尚不能宣布完成的原因

1. 生产脚本导入/持久化/管理尚未接入 production SourceManager；当前 production runtime 启动时创建空 SourceManager。
2. 目标生态 FeiChangDao、XingHai 等脚本的离线真实脚本测试集受上游无明确 LICENSE 阻塞；需要用户提供有权使用的脚本副本或许可。
3. HTMLAudio 与 Wails WebView 的 Go 命令/事件桥已实现；目标设备真实播放及最终媒体请求（headers/Range/redirect/代理）仍需按本方案继续验收。
4. 真实 WebView 音频解码、Range/headers、seek、设备音频输出与两小时内存 soak 需要 Windows 设备验证。

**本轮验收结论：** QuickJS runtime/music 确定性 CI 与定向 desktop 音乐接口测试通过；本轮代码级网络安全/资源边界修复已进入分支。完整真实脚本兼容性审计和音乐模块迁移仍未完成，不能标记整个音乐模块已验收。


### 10.25 当前代码完成度快照（2026-11-23）

本节仅保存本轮审计时的状态与 CI 历史，不定义开发顺序、阶段依赖或验收门。唯一开发顺序以第 4 节 A→B→C→D→E→F→G 为准；本节状态变化必须同步回写第 4 节，不得在此维护第二份计划。

| 能力域 | 当前判定 |
|---|---|
| QuickJS-NG 基础 runtime | 基础实现已有，专项 CI 覆盖 native smoke 与确定性 runtime/music 测试 |
| 洛雪宿主 API | 部分实现；不能据此宣称全量洛雪兼容 |
| HTTP 安全边界 | SSRF、DNS 校验/连接 pinning、redirect、代理/TLS 收敛及资源限制已有实现和测试 |
| SourceManager/播放 URL facade | A/B/C 已完成；D 正在接入 HTMLAudio 实际消费 C 阶段结构化资源 |
| 搜索/详情/歌词/封面 action | B 阶段已完成；生产 search/detail/lyric/pic 入口、Track 模型接线及原生 musicUrl 隔离通过 CI Run #567 |
| HTMLAudio 与播放桥接 | D 实施中；生产构造路径已切换到 HTMLAudio 单例桥，CI/Windows 实播待验收 |
| 媒体代理与特殊请求头 | 未完成端到端验收 |
| Windows 实播、两小时内存稳定性 | 待目标设备验收 |
| 当前执行项 | A/B/C 完成；D 代码、CI 与 Windows PoC 构建通过，目标设备实播待验收 |

历史 CI 记录应以对应 GitHub workflow 的最终 conclusion 为准；workflow 尚在运行时不得记录为成功。

### 10.26 B 阶段 action 调度接线进度（2026-11-23）

- 分支提交：`a2e35d8be0c259828f571c0ccd9be50523113859`。
- `MusicService` 通过生产 runtime 的 `HasExtensionAction/DispatchExtensionAction` 检查并调度社区扩展 action；原生 `musicUrl` 仍由原生 resolver 处理。
- `scriptAwareSourceSearcher` 接入单音源搜索、全局多源搜索和弹幕点歌搜索；支持并发来源请求、来源错误独立记录、搜索结果归一化为 `music.Track`。
- `GetLyrics` 优先使用已声明的 `lyric` 扩展 action；另提供 `GetLXTrackDetail/GetLXTrackCover` 服务入口。
> **历史状态快照，已被下方 10.27 最终验收记录取代。** 当时记录的 CI Run #550 尚未结束、B 尚未验收；后续已完成 detail Track 映射、按音源路由、错误/取消及多脚本隔离，并由 CI Run #567 全量通过。当前状态以第 4 节、第 7 节及 10.27 最终验收记录为准。
- 原历史实现曾要求用户手动选择 `search/detail/lyric/pic`；该交互已废弃。当前导入流程应由 QuickJS 初始化脚本并读取 `inited.sources[*].actions` 自动识别社区扩展能力，`musicUrl` 始终只走洛雪原生 action。
- 自建 production fixture 已覆盖 search/detail/musicUrl/lyric/pic 的真实 QuickJS → SourceManager → MusicService 调用。

### 10.27 B 阶段最终验收记录（2026-11-23）

- 最终分支提交：f38a21795e16cacc05ab05f9a9aeeeace726cb6a。
- SourceManager 为每个脚本读取独立的 inited.sources，按 source/action 选择脚本；多个脚本声明相同扩展 action 时，不再按导入顺序跨音源误执行。
- 原生 musicUrl 仅通过原生 resolver 调度，并要求对应 source 在原生 actions 中声明 musicUrl；社区扩展 search/detail/lyric/pic 仍由 My-LiveHub 显式能力声明控制。
- 当脚本未声明或不支持当前 source 时跳过该脚本；无适用扩展时回退 Go provider。脚本执行失败仍按脚本隔离继续尝试其他适用脚本；caller timeout/cancel 优先传播，不聚合成普通脚本失败。
- 新增多脚本/多音源路由、原生 URL source 隔离、未声明 musicUrl 拒绝等 production tests；既有 production MusicService fixture 覆盖 search/detail/musicUrl/lyric/pic、统一 Track 映射、错误分类、取消和脚本隔离。
- CI Run #567（45134122112）最终 success：QuickJS-NG native build、deterministic runtime/music tests、lxproduction tests、desktop music integration、Wails bindings、frontend dependency install、frontend build 全部通过。
- CI #565/#566 暴露的两处测试问题（测试文件漏导入 errors、kg fixture 未返回自身 musicUrl）均已修复；没有删除或放宽验收测试。
- **B 阶段完成。下一阶段唯一为 C：播放 URL、音质回退与缓存闭环。** 本记录只证明 B 的 action/Track 调度范围，不代表 HTMLAudio 实播、完整洛雪宿主兼容或整个音乐模块改造完成。

### 10.28 D 阶段 HTMLAudio PoC 与正式播放桥接（2026-11-23）

- 开始基线：`9bb9de8d1dae49ca8ebf4d0c660f7f9ca9ad610e`；分支：`feat/lx-js-runtime-quickjs-poc`。
- 生产接线：`main.go` 在主窗口创建后调用包级 `AttachHTMLAudioPlayback`；该 helper 在 MusicService 启动前将生产播放端口切换为 HTMLAudio，避免把回调型 setter 暴露给 Wails binding generator。
- 新增 `app/internal/desktop/html_audio_engine.go`：实现资源感知 `LoadResource`、单调递增 session ID、Go → WebView load/play/pause/stop/seek/volume 命令、前端事件回报、快切歌 URL 解析取消、过期 session 丢弃、媒体错误单次刷新重试及刷新期间保持 Loading 防止队列误推进。
- HTMLAudio 直连资源若携带媒体 headers 或要求 proxy，当前引擎 fail-closed，不会把脚本 API headers 冒充成媒体 GET headers；受控媒体传输、Range、redirect/DNS pinning 属于 E 的工作。
- 新增 `app/frontend/src/services/htmlAudio.ts`：应用级单例 `Audio`，从 `main.ts` 注册 Wails `music:html-audio:command`，处理 load/play/pause/stop/seek/volume；把 loadstart/playing/pause/timeupdate/durationchange/waiting/stalled/ended/error/autoplayblocked 回传 MusicService，timeupdate 限频 250ms。
- MusicService 增加内部 HTMLAudio 初始化/事件 emitter、公开 `ReportHTMLAudioEvent/Seek/SetVolume`；MusicView 接入进度、音量、暂停/继续/停止控件，并将旧洛雪 API 配置明确标记为兼容保留，不再显示虚假的“连接测试”。
- 新增测试：资源感知队列加载、资源身份拒绝、媒体 headers/proxy fail-closed、单次 URL 刷新、刷新期间不提前推进队列、过期 session 事件丢弃、快速切歌取消旧 URL 解析、HTMLAudio 空队列正常结束。所有新测试已加入 QuickJS Windows workflow 定向测试表达式。
- CI 修复记录：先修复 HTMLAudio 空队列仍调用旧洛雪 `NextTrack` 的失败；随后修复 TypeScript 模块声明的换行转义，以及 MusicView 两个 slider handler 的隐式 any。没有删除或放宽测试。
- 最终代码提交：`3d8f65960deea3e85863ef700242a216f0431861`；CI Run `45149682218` 成功，QuickJS-NG native build、deterministic runtime/music tests、production QuickJS tests、desktop HTMLAudio tests、Wails bindings generation、npm ci、Vue TypeScript 检查及 Vite build 全部通过。
- Workflow/方案记录提交：`c067e03a472ac6b5b9a24a6e91bc2fa3f1bf637f`；CI Run `45150651612` 成功，额外完成 Windows `lxproduction` 可执行文件构建和 artifact 上传。Artifact：`My-LiveHub-HTMLAudio-D-PoC`，ID `14806154129`，约 17.9 MB，保留至 2026-11-30。
- 目标设备验收包可从 [GitHub Actions Run 45150651612](https://github.com/dongguo4812/My-LiveHub/actions/runs/45150651612) 的 Artifacts 下载；在目标 Windows/WebView 设备验证真实来源播放、暂停/恢复、seek、volume、stop、切歌、错误恢复，并记录 WebView 版本和媒体格式。
- **当前验收状态：D 尚未达到整体停止条件。** 自动化代码/CI 子门已通过；真实 Windows WebView 可听播放及设备控制验收仍待目标设备执行。未取得该证据前不得勾选 D 完成门或进入 E。


### 10.29 社区扩展 action 自动识别与导入反馈（2026-11-23）

- 移除 JS 音源导入页的社区扩展 action 手动勾选项；用户只需选择在线/本地脚本并点击导入。
- 导入前通过独立 QuickJS capability probe 执行脚本初始化，读取洛雪握手数据 `inited.sources[*].actions`，自动识别 `search/detail/lyric/pic`，并将识别出的能力写入脚本存储和导入结果。
- `musicUrl` 不进入社区扩展能力集合，单独识别并记录为洛雪原生 action，由原生 action resolver 处理；仅声明 `musicUrl` 的脚本仍属于具备有效能力的脚本，允许导入、自动启用并展示原生能力。
- 没有 `inited.sources`、没有有效音源声明、或既未声明支持的社区扩展 action（`search/detail/lyric/pic`）也未声明支持的原生 action（`musicUrl`）时，导入明确失败并返回实际握手声明原因；不得静默保存为无能力脚本。
- 导入成功后展示识别出的社区能力和支持音源；脚本列表持续展示已识别能力、音源范围及 musicUrl 原生处理说明。
- 自动识别使用初始化握手声明作为能力依据，不通过源码字符串搜索猜测函数存在；该识别仍需由真实脚本兼容性测试验证。CI 尚未完成前不得将本节标记为验收通过。

### 10.30 Windows QuickJS 环境依赖去 CGO 化（2026-11-23，实施中）

**目标：** Windows 开发者执行 `task dev` / `task build` 不需要自行安装 MSYS2、MinGW-w64 或 GCC，同时不能关闭生产 LX runtime、不能退化为 `lx_script_runtime_other.go` 的空实现，也不能丢失 QuickJS 的 Go Host API。

**实施架构：**

- QuickJS-NG 仍固定为 v0.18.0；C shim 与 QuickJS 静态链接成独立 `lxqjs.dll`，由 CI 的 MSYS2 UCRT64 工具链构建。
- Go 侧使用 `github.com/ebitengine/purego` 动态加载 DLL 并绑定 C ABI，生产构建不再使用 `import "C"`。
- 原有 Go 回调改为显式函数指针注册，保留 MD5、AES、RSA、随机数、Buffer、zlib；跨边界内存由 DLL 自己的 allocator 分配和释放，避免不同 CRT allocator 混用。
- QuickJS DLL 由 CI 发布为固定 ABI 版本的 Release asset；Windows PowerShell 开发入口自动下载、校验 SHA256 并放入 `app/bin`。
- Taskfile 的 `dev/build` 均设置 `CGO_ENABLED=0`。本机不需要 C 编译器；CI 仍使用编译器生成预编译 DLL。

**停止条件（必须全部满足）：**

1. Windows x64 `CGO_ENABLED=0` 下，QuickJS native shared library 构建成功，导出符号可被 Go 动态绑定。
2. QuickJS runtime/music 全量确定性测试、`lxproduction` 测试和 desktop 音乐集成测试通过；回调覆盖 MD5/AES/RSA/random/Buffer/zlib。
3. Windows Wails 生产可执行文件在无 CGO 条件下构建成功，运行时 DLL 与应用正确配套。
4. CI 发布 DLL 后，移除本地 DLL，再执行与 `task build` 相同的 PowerShell 引导流程；自动下载、SHA256 校验、安装 DLL 并完成 Wails 构建。
5. Taskfile 的 `dev/build` 均不再要求本机 GCC；缺少/下载失败/校验失败时明确报错，不回退到空 runtime。

**验收结果：** 停止条件已满足。Windows x64 CI Run [45168447541](https://github.com/dongguo4812/My-LiveHub/actions/runs/45168447541) 的功能步骤全部成功：QuickJS DLL 构建与 C API smoke test、CGO_ENABLED=0 的 lxruntime/music 测试与 go vet、lxproduction 测试、desktop 集成测试、Wails Windows 构建，以及删除本地 DLL 后从 Release 自动下载、SHA256 校验并再次完成 Wails 构建。该 run 仅剩 GitHub Actions 的 post-job 清理步骤，不影响上述功能步骤结果。

当前代码提交：`a2f184c5201090f41467cc503e8422c3c6c1c49b`。预编译运行时发布地址：[QuickJS-NG v0.18.0 / LX ABI 1](https://github.com/dongguo4812/My-LiveHub/releases/tag/quickjs-ng-v0.18.0-lxabi1)。

**结论：** Windows 开发者执行 `task dev` / `task build` 不再需要本机安装 GCC、MSYS2 或 MinGW；Taskfile 自动准备预编译 DLL，Go 生产 runtime 在 CGO_ENABLED=0 下运行，未退化为空 runtime。CI 仍使用编译器生成 DLL，这是构建机依赖，不是开发者本机依赖。


## 10.31 LX 脚本导入能力探测初始化超时修复（2026-11-23）

**问题证据：** 用户导入并启用脚本时，能力探测先后出现 `JavaScript execution exceeded 3s` 与真实样本 `fish/latest.js` 的 `JavaScript execution exceeded 15s`。QuickJS 每次同步 `Eval` 有独立超时；即使整体初始化有 60 秒 context deadline，单次源文件求值仍可能先被 Eval 超时中断。

**修复与边界：**
- 导入能力探测 Runtime 的单次 Eval 上限提升至现有请求预算上限 `lxMaxRequestTimeout`（60 秒）；普通运行时及 action 执行仍保留默认 3 秒同步 Eval 上限，不全局放宽脚本执行限制。
- 探测整体仍受 60 秒 context deadline、默认 32 MiB QuickJS 内存限制和 512 KiB 栈限制约束；HTTP 请求安全策略、请求数量及响应大小限制不变。
- 60 秒不是对任意脚本无限放行：超出预算仍中断并拒绝导入。真实混淆脚本若仍超时，必须继续定位其同步执行热点或兼容性问题，不能仅以增加超时宣称兼容。
- 能力识别仍以真实执行后的 `inited.sources[*].actions` 为唯一依据，不通过静态扫描猜测能力；初始化失败仍明确拒绝导入。

**真实样本验收：** 已将用户提供的 `https://gh-proxy.org/raw.githubusercontent.com/pdone/lx-music-source/main/fish/latest.js` 加入 opt-in Windows QuickJS CI 集成测试，覆盖代理下载、正式导入入口、自动启用、持久化注册及音源/action 识别。首次真实执行在 15 秒 Eval 上限被中断，证实 15 秒仍不足；当前改为 60 秒受限探测，等待下一轮 CI 给出结果。fixture 测试通过不能替代真实样本验收。