# My-LiveHub 独立音乐播放器改造方案（Go + QuickJS-NG + HTMLAudio）

> 状态：开发基线方案  
> 目标分支：`feat/lx-js-runtime-quickjs-poc`  
> 最终架构：Go + QuickJS-NG + HTMLAudio + Vue/Wails WebView  
> 核心架构约束：继续使用洛雪 JS 音源脚本；通过 QuickJS-NG 模拟洛雪 JS 运行时环境，兼容导入脚本所需的宿主 API/全局对象/请求行为，实现搜索、详情、播放 URL、歌词等能力；HTMLAudio 只负责最终音频播放。复用现有音乐业务层，不引入独立播放器进程。

> **当前执行快照（2026-11-26，按当前分支代码、CI 和本项目对话中的用户验收结果更新）**
> - 当前开发分支：`feat/lx-js-runtime-quickjs-poc`；本文件位于 `docs/MUSIC_PLAYER_HTMLAUDIO_MIGRATION_PLAN.md`，是本分支音乐播放器迁移进度的记录文件。
> - A 阶段生产脚本导入、持久化、启停/删除、损坏脚本隔离及 QuickJS production runtime 生命周期已完成，CI Run #544 通过。
> - B 阶段统一 action 调度与歌曲模型接线已完成：生产搜索/详情/歌词/封面 action、按脚本 `inited.sources` 路由、原生 `musicUrl` 声明校验、Track 映射、错误分类和多脚本隔离均已实现，CI Run #567 通过。
> - C 阶段结构化 `PlaybackResource`、音质回退、TTL 缓存、资源身份校验、资源感知引擎端口及幂等媒体失败刷新已完成，CI Run 45142573201 通过。
> - D 阶段生产 HTMLAudio PlaybackEngine、Wails 命令/事件桥、应用级单例 Audio、播放控制、错误回传、失效 URL 单次刷新及切歌取消已完成；CI Run 45602383401 通过，用户本地目标环境实播验收通过。
> - E 阶段媒体传输适配、旧播放器播放引擎/外部播放器依赖替换、旧配置迁移和设置页收尾已完成；Desktop validation Run #2928 全部通过。这里的“旧播放引擎清理”不等于全面清理洛雪 JS/运行时兼容实现；**旧洛雪实现的全面清理按用户要求留到所有迁移与验收工作最后执行**。
> - F 阶段歌词系统和播放队列/稳定性清单已完成：切歌取消旧歌词请求、歌词同步与窗口状态、播放队列优先级、观众点播抢占及结束后恢复、歌单播放模式持久化等已完成核查和验证；相关 CI 已通过。该状态不代表每个第三方音源在任何时刻都保证可用。
> - G 阶段已完成多项代码和 CI 工作：MusicService/QuickJS 后台初始化及失败诊断、播放首次解析与 fallback 修复、响度标准化及音频重音/回声问题收尾、WebView2 宿主清单 `asInvoker` 统一嵌入等。Desktop validation Run #2863、#3109、#3110 等相关 CI 通过。
> - WebView2 进程归属：已统一开发/CI/发布构建的 manifest，CI #3109/#3110 通过；**尚无 Windows 10 实机任务管理器验收**。Windows 10「进程」页按系统行为显示宿主应用组，不支持像新版 Windows 11 那样逐个展开显示 WebView2 子进程；不能把这一 UI 限制描述为代码已实现。
> - G 阶段仍未完成：目标 Windows 设备连续播放至少 2 小时的 soak、主进程与 WebView renderer 内存基线/增量实测、最终稳定性与资源释放验收。CI 通过不替代这些设备实测。
> - 歌曲搜索页音源选择器按启用状态/优先级排序、可用音源置底并默认选中首项的优化已完成，不再列为待办。
> - **当前阶段：G 的剩余设备稳定性/性能验收。A～F 已完成；G 部分完成。旧洛雪实现全面清理排在所有迁移阶段及最终验收之后，当前不启动。**

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
- `app/internal/desktop/music_service.go` 的 `MusicService` 管理音源注册、队列、播放状态、歌词缓存及脚本运行时；默认播放端口为 My-LiveHub 自己的 HTMLAudio 引擎。
- 旧 `lx_playback_engine.go`、平台启动器及对应测试已删除；播放控制不再访问外部播放器。
- `app/internal/config/music.go` 已移除 `LXAPIEndpoints`；配置版本升级至 v18，加载旧配置时会丢弃 `lx_api_endpoints`，并保留其他用户设置。
- `app/frontend/src/views/MusicView.vue` 已有音乐页面、搜索、队列和播放交互，可在此基础上接入统一 HTMLAudio 播放器，不另造整套页面。
- `app/internal/music/lifecycle.go` 定义了播放引擎生命周期接口。

以上是代码结构事实。当前阶段状态：**D 的生产 HTMLAudio 桥接、自动化测试及用户本地目标环境测试均已通过，D 阶段已完成。**

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

**当前代码状态：D 阶段已完成。** `main.go` 生产构造路径已通过 `AttachHTMLAudioPlayback` 切换到 session-aware HTMLAudio PlaybackEngine；单例 Audio、Wails 命令/事件桥、播放控制、媒体失败刷新和切歌取消已接通。CI Run 45602383401 的真实音源解析、Chromium HTMLAudio 播放/歌词测试和正式客户端构建验证均成功，且用户已完成本地目标环境测试并确认通过。

**工作内容**
- 在现有 Wails WebView/Vue 页面创建唯一、可复用的 HTMLAudio 实例，使用 C 阶段真实脚本解析资源验证可听见播放。
- 定义 Go MusicService 与前端 Audio 的 load/play/pause/resume/seek/setVolume/stop/snapshot 命令及事件契约。
- 为播放会话分配递增 generation/session ID，丢弃旧会话迟到的 URL 结果和音频事件。
- 处理自动播放拒绝、网络中断、HTTP 错误、格式不可解码、ended/error 和设备输出异常。
- 记录 Windows/WebView 版本、资源格式、请求响应特征及实播结果。

**完成门**
- [x] Windows 目标 WebView 真实来源可听见播放，并完成基本控制；用户本地测试已确认通过。
- [x] 快速切歌、暂停/恢复、跳转、音量、停止、错误和迟到事件处理通过测试。
- [x] 不启动洛雪客户端或外部播放器；相关构建与 CI 检查通过。

### E. 媒体传输适配与旧引擎替换

**当前代码状态：E 阶段已完成。** Desktop validation Run #2928 全部作业通过，覆盖 Go 全量测试、生产 LX Runtime 测试、前端构建、Windows 主程序构建，以及真实音源与 Chromium HTMLAudio/歌词集成。生产主窗口使用 HTMLAudio；媒体代理及 Range、取消、重定向校验、连接 pinning 和流式转发已有实现。按用户决定，逐项媒体传输真实音源验收不再作为 E 阶段阻塞任务，后续仅在发现具体音源兼容问题时针对性处理。旧配置迁移与设置页收尾已完成：配置版本升级至 v18，旧 `lx_api_endpoints` 字段会在加载时移除且不丢失其他音乐设置；设置页不再展示旧 API 地址。旧播放引擎、平台启动器、外部播放状态轮询和外部队列控制已从生产播放链路清理。**注意：这只表示旧播放器播放依赖已替换，不表示旧洛雪 JS/运行时兼容实现已全面清理；后者按用户要求留到所有迁移与验收工作最后执行。**

**工作内容**
- [x] 保留现有受控媒体代理实现；不再要求逐音源开展独立的真实传输验收，出现具体兼容故障时再针对性补齐。
- [x] 禁止整曲预下载；代理继续执行 DNS 校验、重定向限制、请求大小/并发限制。
- [x] 清理生产播放路径及代码库中对旧 LX 播放引擎、洛雪本地 API 状态轮询和 `lxmusic://` 启动协议的旧依赖；Desktop validation Run #2928 全绿。
- [x] 旧配置升级至 v18 时移除 `lx_api_endpoints`，并保留既有 music.json 的其他用户设置；设置页不再展示旧 API 地址。
- [x] 增加旧配置迁移回归测试，覆盖音量、响度标准化、点歌/切歌命令、队列参数、歌单模式、音源优先级、粉丝勋章和旧 API 地址兼容数据。

**完成门**
- 不安装/启动洛雪客户端时，现有搜索、队列、播放控制、自动切歌可完整工作。
- 媒体传输真实音源逐项验收不再作为阶段门；如出现具体播放兼容问题，按问题补充验证和修复。旧播放依赖从生产链路移除。
- [x] 旧配置迁移回归测试、设置页调整及 Desktop validation CI 通过。

### F. 歌词、封面、队列与多视图整合

**当前代码状态：已完成（按本项目当前 F 阶段验收清单）。** QuickJS lyric/pic action、五个平台歌词适配器、统一同步 LRC 解析、桌面歌词链路及切歌时取消旧歌词请求已完成核查；播放队列与稳定性清单中的 1～7 项也已完成核查和验证。队列侧歌单播放模式持久化、观众点播优先抢占、点播结束后恢复歌单及中断状态清理均已有代码和 CI 覆盖。真实第三方音源的在线可用性仍受平台/API 状态影响，不作永久可用承诺。

**工作内容**
- 接通脚本 lyric/pic action 与现有歌词、封面服务和缓存。
- 歌词进度以 HTMLAudio 真实 currentTime 为准，验证原文、翻译、逐行/逐字时间戳格式与同步策略。
- 切歌时取消旧歌词/封面任务，清理旧缓存引用，防止旧结果覆盖当前歌曲。
- 验证 Go 队列顺序、插队、删除、重复曲目策略、播放模式和 ended 自动切歌。歌单模式持久化及观众点播抢占/恢复已有实现和 CI 覆盖；仍需补齐完整 UI/真实播放场景回归。
- 主播放器、迷你播放器、独立歌词窗口共享同一播放状态和同一个 Audio 实例。

**完成门**
- 歌词与实际播放时间同步，无明显累计漂移；封面/歌词失败可诊断。
- 队列操作、自动切歌和播放状态一致；多窗口/多视图无重复播放或状态分叉。
- 切歌后旧歌词和旧事件不会残留；相关测试与 CI 通过。

### G. 安全、稳定性、性能与最终验收

**当前代码状态：部分完成。** QuickJS HTTP 安全边界、SSRF/DNS rebinding、redirect、headers、请求资源限制及部分生命周期测试已有自动化覆盖；MusicService/QuickJS 后台初始化、播放解析 fallback、响度标准化及音频重音/回声收尾也已完成相应代码和 CI 验证。WebView2 宿主清单已统一为 `asInvoker`，CI #3109/#3110 通过，但没有 Windows 10 实机任务管理器验收；Windows 10「进程」页本身不提供新版 Windows 11 的逐个 WebView2 子进程展示能力。仍未完成的 G 阶段验收重点是目标设备连续播放至少 2 小时、主进程与 WebView renderer 内存基线/增量实测，以及最终稳定性和资源释放验收。

**工作内容**
- 对脚本隔离、超时/取消、SSRF/DNS rebinding、redirect、headers、请求/响应体、并发和资源释放做最终回归审查；所有生产网络请求复用受控出口。
- 完成搜索缓存、播放 URL 缓存、歌词缓存容量/TTL/失效策略和有限重试。
- 验证停止/切歌后请求取消、Audio 事件监听清理、对象引用释放和异常恢复。
- 分别测量 My-LiveHub 主进程、WebView renderer 的基线与音乐模块增量，覆盖空闲、搜索、播放、歌词+队列、多音源并发和停止释放。
- 在目标 Windows 设备连续播放至少 2 小时，记录稳定期内存、播放错误、切歌及停止后的内存回落。
- [x] 全仓检查旧洛雪本地 API、`lxmusic://`、外部播放器进程和旧引擎引用；旧播放代码与生产依赖已删除，Run #2928 验证通过。

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
| C. 播放 URL、音质回退与缓存闭环 | **已完成** | 结构化 PlaybackResource、音质回退、TTL 缓存、资源身份校验、资源感知引擎端口、幂等媒体失败刷新均已实现并通过 CI Run 45142573201；HTMLAudio 消费资源及媒体错误刷新另由 D 覆盖 |
| D. HTMLAudio PoC 与正式播放桥接 | **已完成（用户本地测试通过）** | 生产 HTMLAudio 单例、Wails 桥、播放控制、session 保护、错误刷新及 Chromium 实播 CI 已完成；用户已确认 D 阶段本地测试通过 |
| E. 媒体传输适配与旧引擎替换 | **已完成** | 媒体代理、Range/取消/重定向/连接 pinning、旧播放器播放依赖替换、旧配置迁移和设置页收尾完成；Desktop validation Run #2928 全绿。旧洛雪 JS/运行时全面清理不属于此项，按用户要求留到最后 |
| F. 歌词、封面、队列与多视图整合 | **已完成** | 歌词系统完善及播放队列/稳定性清单 1～7 项已完成核查验证；包含切歌取消旧歌词请求、同步歌词/桌面歌词链路、歌单模式持久化、观众点播优先及完成后恢复；相关 CI 通过 |
| G. 安全、稳定性、性能与最终验收 | **部分完成** | QuickJS HTTP/SSRF/DNS rebinding/redirect/headers/请求资源限制、启动优化、播放解析/fallback、响度标准化与音频问题收尾、WebView2 manifest CI 已完成；仍缺目标设备 2 小时 soak、主进程/renderer 内存实测及最终设备稳定性验收。Windows 10 任务管理器逐个显示 WebView2 子进程不属于可由应用实现的验收项 |

### 当前阶段状态

A、B、C、D、E、F 阶段均已完成。G 阶段部分完成，当前只推进 G 尚未完成的设备稳定性与性能验收：目标 Windows 设备连续播放至少 2 小时、主进程与 WebView renderer 内存基线/增量实测、停止/切歌后的资源释放与最终稳定性核查。CI 通过不替代设备实测。旧洛雪 JS/运行时实现的全面清理明确排在 A～G 迁移及最终验收之后，当前不启动。

**旧洛雪 JS/运行时实现的全面清理属于最后收尾工作，按用户明确要求，必须等 A～G 迁移与最终验收全部结束后再启动；不得将 E 阶段已完成的旧播放器依赖替换误认为这项全面清理已经完成。**

**以下“阶段 0”标题及内容均为历史审计记录，保留其当时的源码证据和技术结论；它们不再代表当前阶段、开发顺序或下一步。**

### 阶段 0 已确认的关键调用链（2026-11-23）

本轮继续检查后确认：

1. `MusicView.vue` 的搜索结果通过 `Enqueue` / `PlayTrackNow` 交给 Go；前端没有直接的 Audio 播放实现。
2. `MusicService.playNext()` 调用 `resolveAndStartLXTrack()`，后者位于 `music_auto_switch.go`，会按已启用音源优先级尝试候选歌曲，并直接调用 `s.engine.Load(ctx, candidate)`。
3. 播放 URL 解析已接入 My-LiveHub 自己的 HTMLAudio 播放链路：Go 通过现有脚本运行时解析结构化 PlaybackResource，再交给 HTMLAudio 执行媒体加载；不再把 Track 委托给洛雪桌面客户端。
4. `MusicService.watchPlayback()` 每 500ms 读取本地 HTMLAudio 引擎快照；HTMLAudio 的 `ended` 事件会更新快照，Go 据此推进 My-LiveHub 自己的队列。队列为空时保持 idle，不会调用外部播放器的 `NextTrack`。
5. `Pause/Resume/Stop` 当前直接调用 Go PlaybackEngine。新方案需要由前端 HTMLAudio adapter 执行，并将实际结果/事件回传。
6. `GetLyrics(track)` 已按 Track/音源调用脚本 lyric action 或平台歌词适配器；不读取洛雪本地 `/lyric-all`、`/lyric`，也不依赖外部播放器当前曲目。
7. 主音乐页通过 `GetCachedPlaybackStatus()` 读取 Go watcher 缓存；独立歌词窗口也以约 500ms 频率轮询状态，并用本地时钟每 30ms 外推歌词位置。接入 HTMLAudio 后，应优先共享同一播放状态快照/事件，避免 Go watcher 与前端 Audio 各自成为状态权威。
8. `MusicConfig` 当前版本为 18；旧 `lx_api_endpoints` 在配置加载时作为废弃字段清除，迁移测试确保音量、响度标准化、队列、命令、播放模式和音源优先级等用户设置保留。

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

#### C. 已发现的关键架构缺口（2026-11-23 历史审查记录，后续状态见本文 E 阶段更新）

1. **播放 URL 解析已接入主链路。** `playNext → resolveAndStartLXTrack` 复用现有 QuickJS URL resolver，并将结构化 PlaybackResource 交给 HTMLAudio 引擎；不得另写一套脚本解析入口。
2. **播放 URL 资源模型过窄。** 目前 resolver 返回 `string`，丢失媒体响应所需的 headers、MIME/编码提示、过期信息及来源信息。脚本的 API 请求 headers 与媒体 GET headers 是两个不同概念。阶段 0/1 应先定义 `PlaybackResource`，默认不携带可写入日志的敏感 Cookie/签名参数。
3. **HTMLAudio 请求头限制需要实测。** 如果最终媒体 GET 必须携带 Referer/Cookie/自定义 header，单纯把 URL 赋给 Audio.src 可能无法播放。阶段 4 需要依据真实响应决定直连、音源降级或 Go 流代理；代理必须支持 Range、取消、Content-Type、Content-Length/分块传输和重定向语义。
4. **历史问题，已由后续实现解决。** 当时 `GetLyrics` 依赖旧 `PlaybackEngine.GetCurrentLyrics`；后续已改为按 Track/音源调用脚本 lyric action 或平台歌词适配器，旧歌词 API 依赖已删除。
5. **队列推进已由 My-LiveHub 自己管理。** `PlayNext`、`Next` 和播放结束处理只消费 My-LiveHub 的请求队列/歌单；队列为空时进入 idle，不再访问外部播放器队列。

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

### 9.1 总体结论（2026-11-23 历史快照，已被后续阶段验收更新）

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

### 9.2 关键缺口及处理决定（2026-11-23 历史快照；表内“当前事实”不代表当前代码状态）

| 编号 | 缺口 | 当前事实 | 必须采取的处理 |
|---|---|---|---|
| F-01 | 洛雪原生协议与搜索扩展边界 | 已核对的 action 调用链以 musicUrl 为主；现有五平台搜索在 Go provider | 保留洛雪原生脚本 API 兼容层；搜索统一由 JS 脚本执行的需求，采用明确标识的 My-LiveHub 扩展 action（例如 search / detail / lyric），不得伪称这些是洛雪原生 action。对原生脚本缺少扩展 action 的情况提供清晰能力声明，不静默回退到 Go 五平台实现 |
| F-02 | 用户脚本导入与管理 | A 阶段已接入 `config/local/lx_sources.json`、production SourceManager、MusicService 管理 API 和设置页 | 已完成导入、元信息/hash 校验、持久化恢复、启停/删除、损坏隔离及关闭释放；后续只处理 action 调度和扩展能力，不重复建设脚本管理体系 |
| F-03 | 洛雪运行时兼容矩阵 | 已实现部分全局对象、lx API、请求桥接；各 API 的真实覆盖程度仍需逐项核验 | 建立 API 级矩阵，记录状态、代码位置、协议来源、测试和差异；按真实第三方脚本依赖补齐，不能仅凭符号存在就标为兼容 |
| F-04 | 播放资源结构 | 当前 resolver 返回 URL 字符串 | 将返回契约升级为结构化 PlaybackResource，至少包含 URL、source/track/quality、媒体 headers（敏感字段受控）、MIME/编码提示、过期/刷新信息、代理策略和脱敏元数据 |
| F-05 | 媒体传输能力 | HTMLAudio 不能任意设置请求头，真实媒体要求尚未采样 | 先采集真实资源的响应和请求限制；优先直连或脚本音质回退，确有必要才实现支持 Range/取消/重定向/流式响应的受控 Go 代理 |
| F-06 | 导入脚本安全 | QuickJS 无直接 OS API 不代表网络能力安全 | 对 lx.request 实施 SSRF 防护（回环、私网、链路本地、云元数据地址及 DNS 解析后地址校验）、协议/端口策略、超时、响应体上限、并发配额、重定向逐跳校验；对 JS 执行设置内存/栈/时间限制、脚本隔离、取消和资源回收 |
| F-07 | 搜索/详情/歌词契约 | （历史状态）当时搜索是 Go provider，歌词路径依赖旧 PlaybackEngine；后续歌词路径已迁移，搜索/详情能力按后续阶段记录验收 | 先定义 JS action 输入/输出 schema、分页/排序、错误码和 Track 映射；歌词按 Track/source 获取，不依赖外部播放器当前曲目 |
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
| HTMLAudio 目标 WebView 实播 | **已通过** | 用户已完成本地目标环境测试并确认 D 阶段通过 |
| SSRF/脚本资源隔离 | **已有实现与专项测试，仍需后续实测** | DNS/连接 pinning、redirect、代理/TLS 收敛、请求资源限制已有测试；真实外部脚本长时间运行与 soak 尚未验收 |
| 当前执行项 | **A/B/C/D 已完成** | D 阶段 HTMLAudio PoC 与正式播放桥接已由用户本地测试确认通过 |

### 9.7 开发顺序与当前执行项

本节识别的协议、兼容性和 action 调度缺口，全部按第 4 节唯一开发顺序处理，不再设置阶段 0A—0F 等前置子路线。A、B、C、D 已完成；D：HTMLAudio PoC 与正式播放桥接已通过用户本地测试，下一阶段为 E。
