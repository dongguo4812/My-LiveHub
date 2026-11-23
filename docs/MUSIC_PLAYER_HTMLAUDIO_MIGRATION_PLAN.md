# My-LiveHub 独立音乐播放器改造方案（Go + QuickJS-NG + HTMLAudio）

> 状态：开发基线方案  
> 目标分支：`feat/lx-js-runtime-quickjs-poc`  
> 最终架构：Go + QuickJS-NG + HTMLAudio + Vue/Wails WebView  
> 核心架构约束：继续使用洛雪 JS 音源脚本；通过 QuickJS-NG 模拟洛雪 JS 运行时环境，兼容导入脚本所需的宿主 API/全局对象/请求行为，实现搜索、详情、播放 URL、歌词等能力；HTMLAudio 只负责最终音频播放。复用现有音乐业务层，不引入独立播放器进程。

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

以上是代码结构事实，不代表 HTMLAudio 播放能力已经实现。当前阶段状态：**现状已初步核对；HTMLAudio PoC 尚未完成；外部播放器依赖尚未移除。**

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

## 4. 分阶段实施计划与验收门

阶段必须按顺序推进。每个阶段完成代码、测试和 CI 验证后，更新本文进度，再开始下一阶段。CI 失败必须读取具体失败日志、定位根因、修复并重新运行；不能把提交或 CI 启动视为阶段完成。

### 阶段 0：洛雪 JS 运行时兼容性盘点与架构冻结

**工作内容**
- 梳理 `MusicService` 所有依赖 `PlaybackEngine` 的调用点、生命周期和播放状态轮询。
- 梳理前端现有播放控制、歌词、队列、迷你播放器和独立歌词窗口之间的数据流。
- 梳理歌曲搜索结果到播放 URL 解析的完整调用链，确认目前是否已有统一的可用 URL 解析接口。
- 盘点现有 QuickJS-NG 运行时与洛雪运行时兼容层：脚本导入/解析、元信息、全局对象、`lx` API、请求、存储、事件、生命周期及各 action 的实现状态。
- 建立洛雪 JS 运行时 API 兼容矩阵，区分已实现、部分实现、缺失和行为不一致；至少覆盖搜索、详情、播放 URL、歌词所需能力。
- 确认内置脚本和用户导入脚本使用统一加载、校验、执行、隔离和错误处理流程；明确脚本权限、网络访问、超时、取消及资源限制。
- 盘点三音源（当前用户重点验证的三种来源）真实返回的 URL、格式、请求头、Cookie/Referer、有效期和重定向特征。
- 定义 Go ↔ Vue 的播放资源、播放命令、播放事件、错误类型和 session generation 契约。
- 明确 Go 队列与 HTMLAudio ended 事件之间的唯一推进责任，避免重复自动切歌。
- 冻结“洛雪 JS + QuickJS-NG 兼容运行时”是唯一音源脚本架构；HTMLAudio 仅是音频执行器，不替代音源脚本运行时。

**验收标准**
- 形成调用链和文件清单。
- 形成洛雪 JS API 兼容矩阵及运行时缺口清单，明确导入脚本的统一执行路径。
- 明确每个播放状态由谁产生、谁持有、谁展示。
- 确认 PoC 所需的最小前后端接口及测试样本。
- 不改动现有播放行为，不在本阶段删除旧引擎。

### 阶段 1：洛雪 JS 运行时兼容层补齐与脚本导入验证

**工作内容**
- 基于阶段 0 兼容矩阵，补齐真实洛雪 JS 脚本运行所需的 QuickJS-NG 宿主环境，不另造一套不兼容的自定义脚本协议。
- 实现/验证脚本导入、格式校验、元信息读取、加载/卸载、持久运行时、异常隔离、超时和取消。
- 按洛雪 JS 规范实现脚本实际依赖的全局对象及 `lx` API；每个 API 均有契约测试和真实脚本测试。
- 打通脚本 action 调用与统一结果模型，覆盖搜索、详情、`musicUrl`、歌词等核心能力；结果转换由 Go 适配层完成。
- 对内置脚本与至少一个独立导入的第三方洛雪 JS 脚本执行相同测试，证明不是只适配内置脚本。
- 明确脚本请求 headers 与最终媒体请求 headers 的传递边界，敏感数据不进入日志。

**验收标准**
- 可导入、校验、加载、卸载洛雪 JS 音源脚本。
- 内置脚本与独立第三方脚本通过搜索及 action 调用测试；`musicUrl` 能返回经校验的真实播放资源。
- 洛雪运行时 API 兼容矩阵中本阶段定义的核心能力全部有测试证据；未覆盖能力明确列出，不宣称完全兼容。
- QuickJS 异常、超时、取消和脚本间隔离测试通过。

### 阶段 2：HTMLAudio 最小 PoC

**工作内容**
- 在现有 Wails WebView/Vue 页面中创建一个可复用的 HTMLAudio 实例。
- 验证真实 HTTP(S) 音频 URL 的加载与播放，不以 mock 或静音成功作为通过依据。
- 验证播放、暂停、恢复、停止、音量、播放进度和跳转。
- 验证浏览器自动播放策略：由真实用户手势触发首次播放，并正确处理 `play()` Promise rejection。
- 记录 WebView/操作系统版本、音频格式、HTTP 响应头、播放结果和错误。
- 验证页面切换、组件卸载、窗口隐藏/恢复时 Audio 实例生命周期。

**验收标准**
- 在 Windows 目标 WebView 中至少一种真实音源资源可听见地播放。
- 基本控制全部通过。
- 无需启动任何外部播放器进程。
- 播放失败能给出可诊断错误。
- PoC 代码与正式业务集成保持可控边界。

### 阶段 3：统一播放控制与 Go 服务桥接

**工作内容**
- 将 HTMLAudio 封装为前端单例适配器，提供 load/play/pause/resume/seek/setVolume/stop/snapshot 等能力。
- 建立 Go 发起播放命令、前端执行、事件/结果回传的明确契约。
- 为每次播放分配 session generation，阻止切歌竞态、过期 URL 解析结果和迟到事件污染当前状态。
- 将真实 duration/currentTime/paused/ended/error 映射为统一 PlaybackSnapshot。
- 处理音频播放被浏览器拒绝、网络中断、资源不可解码、解码失败和设备输出异常。
- 避免高频 timeupdate 事件造成 Go/Wails 桥接过载；UI 进度更新与业务状态同步采用合理节流。

**验收标准**
- Go MusicService 可控制 HTMLAudio，且播放状态来自真实音频事件。
- 连续快速切歌不会出现旧歌覆盖新歌或重复自动切歌。
- 暂停/恢复/跳转/音量/停止行为正确。
- 不依赖洛雪 API 或系统 URI 协议。

### 阶段 4：现有 Go 音乐业务层整合

**工作内容**
- 保留并复用现有 MusicService、SourceRegistry、Track、Queue、搜索、命令入口和歌词服务。
- 把“播放资源解析”和“音频执行”明确分离：Go 负责解析并交付资源，HTMLAudio 负责播放。
- 将默认 `NewLXPlaybackEngine` 注入替换为 HTMLAudio 桥接实现；如果接口跨越 Go/前端边界不适合直接实现 Go interface，则调整为明确的命令/事件端口，不强行伪造本地 Go 音频引擎。
- 移除播放路径中对 `LXPlaybackEngine.Load`、洛雪本地 API 状态轮询及 `lxmusic://` 启动协议的依赖。
- 保持队列、点歌命令、自动切歌、播放历史和权限逻辑原有语义。
- 梳理配置迁移：旧 `lx_api_endpoints` 数据安全兼容，停止作为播放必需配置；不要因移除旧字段导致已有 music.json 无法加载。

**验收标准**
- 用户通过现有搜索和队列即可播放歌曲。
- 不启动洛雪客户端时，搜索、解析、播放、暂停、切歌完整工作。
- 现有点歌命令和队列行为的回归测试通过。
- 旧配置升级不会丢失队列容量、音源选择、命令和勋章等其他设置。

### 阶段 5：洛雪 JS 多音源真实播放兼容

**工作内容**
- 对目标三音源分别采集真实搜索结果、播放 URL 解析结果和实际 HTTP 响应特征。
- 建立格式兼容矩阵：容器、音频编码、MIME、码率、是否分段/重定向、是否需特殊请求头。
- 在目标 WebView 中逐一实播，不以 URL 返回 200 或 canPlayType 声明支持代替真实播放。
- 对不支持的格式验证音源音质回退策略。
- 对需要特殊请求头或跨域能力的来源，评估 Go 受控流代理；明确代理对 Range、Content-Length、Content-Type、重定向、取消和流式传输的支持。
- 验证 URL 过期后的重新解析、签名失败、音源限流和单音源失败隔离。

**验收标准**
- 三音源至少各有一个真实曲目通过 HTMLAudio 完整播放。
- 记录每个音源的格式与请求限制。
- 不支持格式有可验证的音质回退或明确错误，不允许静默卡死。
- 一个音源失败不影响其他音源搜索和播放。
- 不将整首歌曲预下载到内存。

### 阶段 6：歌词、队列与播放体验

**工作内容**
- 歌词进度以 HTMLAudio 的真实 currentTime 为基准。
- 验证歌词原文、翻译、逐行/逐字时间戳格式与同步策略。
- 切歌时清理旧歌词任务和缓存引用，防止旧歌词覆盖新曲。
- 验证队列顺序、插队、删除、重复曲目策略、自动切歌和播放模式。
- 让主播放器、迷你播放器、独立歌词窗口共享同一播放状态来源。
- 验证窗口隐藏、重新打开和前端路由切换不会创建第二个播放实例。

**验收标准**
- 歌词与实际播放时间同步，无明显累计漂移。
- 队列操作与播放状态一致。
- 多窗口/多视图不重复播放，不出现状态分叉。
- 切歌后旧歌词和旧事件不会残留。

### 阶段 7：缓存、异常恢复与资源释放

**工作内容**
- 区分搜索缓存、播放 URL 元数据缓存、歌词缓存和浏览器自身音频缓冲。
- 设置缓存容量、TTL、失效规则和清理时机；签名 URL 不得超过有效期使用。
- 停止/切歌时中止未完成的 URL 解析、代理请求和旧播放会话。
- 清理 Audio source、事件监听、定时器、对象引用和不再使用的 Blob URL（若使用）。
- 对网络中断、HTTP 错误、格式不支持、URL 过期和音源临时故障设计有限重试与可见错误。
- 避免自动重试造成无限循环、重复计费或反复触发签名请求。

**验收标准**
- 停止播放后不再持续占用旧资源。
- 重复切歌和异常恢复不会持续累积事件监听或对象。
- URL 失效可重新解析，失败可解释。
- 缓存有明确上限，清理行为有测试覆盖。

### 阶段 8：内存与性能优化

**性能目标（设计验收值，非当前实测数据）**

| 场景 | 音乐模块新增内存目标 |
|---|---:|
| 音乐模块已加载、未播放 | ≤15 MB |
| 搜索歌曲、显示结果 | 参考 10–25 MB |
| 正常播放 | ≤35 MB，目标约 20–30 MB |
| 播放、歌词与队列同时运行 | 参考 20–40 MB |
| 多音源并发搜索 | 短时参考 30–50 MB |

**测量要求**
- 分别记录 My-LiveHub 主进程、WebView 渲染进程及其他 WebView 子进程。
- 记录模块启用前基线与启用后增量，不能将整个 WebView 的既有内存都算作音乐模块新增内存。
- 记录 Go heap、前端 JS heap（可用时）、进程 Private Bytes/Working Set、渲染进程内存和播放缓冲相关指标。
- 同一设备、同一 WebView 版本、同一曲目和相同测试步骤进行对照。
- 至少执行空闲、搜索、播放、歌词+队列、多音源并发和停止释放测试。
- 连续播放 2 小时，观察是否存在持续性增长；记录初始预热、稳定期和停止后的回落情况。

**优化方向**
- 搜索结果和歌词按需加载、限制缓存条数。
- 不把音频流整体读入 Go 内存或 JS ArrayBuffer。
- 避免高频 Wails 跨边界调用和无界响应式状态。
- 复用单个 Audio 实例；控制事件监听、轮询和临时对象生命周期。
- 对队列和历史设置合理上限。

**验收标准**
- 空闲和正常播放达到上述目标；若某项受 WebView 共享资源影响无法准确归因，必须提供进程级原始数据和增量计算说明。
- 连续播放 2 小时无持续性内存增长。
- 播放、切歌和歌词同步无明显卡顿。
- 停止后不再使用的模块缓存及时释放。

### 阶段 9：移除旧播放器依赖与最终验收

**工作内容**
- 删除生产路径对洛雪本地播放 API、协议启动和客户端进程启动的依赖。
- 清理仅用于旧播放委托的代码、测试和 UI 配置；保留仍有价值的兼容迁移逻辑，并明确废弃周期。
- 搜索全仓库，确认无意外的 mpv、洛雪启动协议或独立播放器进程调用残留。
- 完成 Windows CI、Go 测试、前端构建、静态检查和真实设备播放验收。
- 更新本文阶段状态、实测结果、已知限制和最终验收结论。

**最终验收标准**
- My-LiveHub 独立运行，不安装或启动洛雪、MusicFree、mpv 等播放器。
- 三音源真实搜索、URL 解析和 HTMLAudio 播放通过。
- 播放控制、队列、歌词、切歌、音质回退和失效恢复通过。
- 旧配置可安全迁移。
- CI 全绿；连续播放 2 小时无持续性内存增长。
- 提供进程级内存测试记录，不以估算值冒充实测值。

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

| 阶段 | 状态 | 证据/备注 |
|---|---|---|
| 阶段 0：洛雪 JS 运行时兼容性盘点与架构冻结 | 进行中 | 已确认现有 ResolvePlaybackURL/QuickJS SourceManager 三脚本链、五个平台搜索、musicUrl 仅返回 URL 字符串、媒体请求 headers 未透传、Go/Vue Wails v3 Events 候选协议；真实媒体响应与 Windows WebView 播放验证待做 |
| 阶段 1：洛雪 JS 运行时兼容层补齐与脚本导入验证 | 未开始 | 当前仅有 QuickJS 执行与部分 LX 宿主 API；须按兼容矩阵补齐并验证第三方导入脚本 |
| 阶段 2：HTMLAudio 最小 PoC | 未开始 | 尚无真实 WebView 播放验收记录 |
| 阶段 3：统一播放控制与 Go 服务桥接 | 未开始 | |
| 阶段 4：现有 Go 音乐业务层整合 | 未开始 | |
| 阶段 5：洛雪 JS 多音源真实播放兼容 | 未开始 | |
| 阶段 6：歌词、队列与播放体验 | 未开始 | |
| 阶段 7：缓存、异常恢复与资源释放 | 未开始 | |
| 阶段 8：内存与性能优化 | 未开始 | 所有内存数值目前均为设计目标，尚未实测 |
| 阶段 9：移除旧播放器依赖与最终验收 | 未开始 | |

### 当前下一步

继续阶段 0，但优先完成洛雪 JS 运行时兼容矩阵：逐项核对导入脚本规范、QuickJS 全局环境、`lx` 宿主 API、请求/存储/事件及搜索/详情/播放 URL/歌词 action。补齐现状和缺口证据后，进入阶段 1 运行时兼容层补齐；运行时与脚本导入验收通过后，才进入阶段 2 HTMLAudio PoC。


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

### 8.3 重排后的阶段和依赖关系

| 阶段 | 目标 | 主要交付物 | 进入下一阶段的门槛 |
|---|---|---|---|
| 0 | 运行时现状盘点与接口冻结 | 洛雪 JS API 兼容矩阵、现有调用链、脚本导入链、缺口列表、资源与事件契约草案 | 明确核心 API 的现状和差距；真实媒体请求限制有采样计划 |
| 1 | 洛雪 JS 运行时兼容层补齐与脚本导入 | 可导入/校验/加载/卸载脚本；补齐核心宿主 API；统一 action 执行器；第三方脚本测试 | 内置及至少一个第三方脚本的搜索、播放 URL action 测试通过 |
| 2 | HTMLAudio 最小 PoC | 单例 Audio、真实 URL 播放、基础控制、WebView 生命周期验证 | 目标 Windows Wails WebView 中真实可听播放 |
| 3 | Go ↔ Vue 播放控制桥接 | 命令/事件协议、session generation、快照和错误模型 | 快速切歌、迟到事件、窗口广播测试通过 |
| 4 | 现有音乐业务层整合 | MusicService 接入脚本解析与 HTMLAudio；替换旧 LXPlaybackEngine 默认路径；旧配置迁移 | 搜索→脚本解析→播放→队列闭环，无洛雪客户端依赖 |
| 5 | 多脚本/多平台真实播放兼容 | 脚本×平台×音质测试矩阵；请求头/格式验证；必要时 Go 流代理 | 目标平台真实播放、回退、过期刷新及失败隔离通过 |
| 6 | 歌词、队列与多窗口体验 | 脚本歌词 action、Audio currentTime 同步、唯一队列和多窗口状态 | 歌词、切歌、队列及窗口状态一致 |
| 7 | 缓存、异常恢复与资源释放 | TTL/容量、取消、重试、资源清理 | 无无界缓存、旧会话泄漏或无限重试 |
| 8 | 内存与性能验收 | 分进程基线/增量数据、2 小时播放记录 | 达到既定内存目标或给出可复核的偏差说明；2 小时无持续增长 |
| 9 | 清理旧播放器依赖与最终验收 | 旧客户端播放依赖清理、全量 CI、实机验收报告 | 所有功能、兼容、性能和迁移门槛通过 |

### 8.4 重新明确的开发顺序

- 先盘点并补齐**洛雪 JS 运行时兼容能力**，再做 HTMLAudio PoC；不得先把 HTMLAudio 做成孤立播放器，再发现脚本运行环境无法提供资源。
- 运行时阶段先覆盖核心脚本协议和宿主 API，同时保留缺口清单；不要为了追求“全量兼容”而无边界扩张，也不能把只支持内置脚本称为通用导入能力。
- HTMLAudio PoC 使用由洛雪 JS 脚本真实解析得到的媒体 URL；mock URL 仅可用于单元测试，不可作为阶段验收。
- 业务整合复用现有 Go 搜索、统一歌曲模型、SourceRegistry、QuickJS resolver 和队列；不得另建一套平行音乐业务层。
- 真实媒体请求所需 headers 必须由脚本解析结果明确传递到 PlaybackResource；脚本请求 headers 与媒体 GET headers 不可混为一谈。
- 每个阶段只在验收证据齐全后标记完成。CI 通过不代表第三方脚本兼容、真实媒体播放或性能验收通过。

### 8.5 当前状态校正

截至本次文档修订：

- 已确认项目继续使用洛雪 JS 脚本，QuickJS-NG 作为执行引擎，必须通过兼容层模拟洛雪运行时环境。
- 已确认现有代码具备部分 QuickJS/LX runtime、三个内置脚本的 `musicUrl` URL 解析链和五个平台 Go 搜索适配器；这**不等于**完整洛雪运行时兼容，也不等于通用 JS 导入能力已经验收。
- 现有 `MusicService.ResolvePlaybackURL` 尚未接入当前旧播放器播放主链路；后续必须复用并扩展资源模型。
- HTMLAudio 尚无目标 Windows WebView 的真实播放验收证据。
- 阶段 0 仍为进行中；阶段 1 至阶段 9 均未开始。不得因本次文档更新改变代码实现状态。

**本次修订只更新架构决策和开发计划，没有声称已经完成运行时兼容、第三方脚本导入或 HTMLAudio 实播。**
