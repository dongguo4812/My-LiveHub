# 洛雪点歌链路审计与 My-LiveHub 音乐模块重构方案

> 基准分支：My-LiveHub dev
> 基准提交：d70ead9e57229acbb5bf33197e5c588c68be3984
> 对照上游：lyswhut/lx-music-desktop master
> 状态：源码审计第一轮完成；运行时重构尚未开始

## 1. 目标与边界

My-LiveHub 保留自己的 Go/Wails 桌面宿主、统一播放器和 mpv 播放引擎。洛雪作为音乐业务行为与自定义音源协议的参考实现，不把 Electron 整个播放器搬进项目，也不把 QQ/网易云等平台各自变成独立播放器。

必须分开四类职责：

1. 搜索适配器：搜索歌曲并返回平台原生元数据。
2. 歌曲模型：保存统一字段及完整的平台专属字段。
3. 播放解析器：根据歌曲所属平台、音质和解析器能力返回可播放资源。
4. 播放协调器：管理队列、当前歌曲、解析重试、跨平台匹配、mpv 状态与切歌。

搜索成功、入队成功、URL 解析成功、mpv 真正开始播放是四个不同状态，不得混为一个“播放成功”。

## 2. 洛雪真实点歌与播放链路

已核对的上游代码：

- src/renderer/utils/musicSdk/index.js
- src/renderer/utils/musicSdk/tx/musicSearch.js
- src/renderer/utils/musicSdk/tx/index.js
- src/renderer/utils/musicSdk/api-source.js
- src/renderer/utils/musicSdk/api-source-info.ts
- src/common/utils/tools.ts
- src/renderer/core/music/utils.ts
- src/renderer/core/music/online.ts
- src/renderer/core/music/index.ts
- src/renderer/core/apiSource.ts

### 2.1 搜索与跨平台匹配

洛雪 musicSdk.searchMusic 并发调用已注册平台的搜索模块，排除当前来源及显式排除的平台，收集各平台结果。其 findMusic 并非简单比较标题：会规范歌手分隔符、过滤标点和空白、比较歌曲名/歌手/专辑/时长，并考虑版本字符；匹配结果再按多组优先级排序。

因此，My-LiveHub 的搜索结果聚合可以继续服务于管理界面，但播放失败后的跨平台匹配不能直接复用通用标题排序或严格标题相等逻辑，必须实现与洛雪 findMusic 对齐的专用匹配流程。

### 2.2 QQ 搜索对象

洛雪 QQ 搜索只接受带 item.file.media_mid 的结果。音质由实际文件大小生成：

- size_128mp3 → 128k
- size_320mp3 → 320k
- size_flac → flac
- size_hires → flac24bit

每个可用音质同时进入 types 数组和 _types 对象。QQ 平台旧式歌曲对象的关键字段来自原始搜索结果：

- songId = item.id
- songmid = item.mid
- albumId = item.album.mid（字符串）
- albumMid = item.album?.mid ?? ''（字符串）
- strMediaMid = item.file.media_mid
- interval = formatPlayTime(item.interval)
- typeUrl = {}

专辑缺失时，洛雪先以空字符串初始化 albumId 和 albumName；不能自行用数字专辑 ID 填充 albumId。

### 2.3 新旧歌曲对象转换

洛雪 toNewMusicInfo 将旧式对象转换为带 meta 的统一对象；toOldMusicInfo 在调用平台 SDK 或用户自定义 API 前重新生成旧式扁平对象。

QQ 的 toOldMusicInfo 会从 meta 还原 strMediaMid、albumMid、songId，并由通用逻辑还原 songmid、albumId、types、_types、interval、albumName、img、typeUrl。这份扁平对象是自定义音源边界的数据契约。

### 2.4 音质与播放地址

洛雪先使用显式传入的音质；没有显式音质时，根据用户音质设置和歌曲实际 _qualitys 能力选择。然后调用：

~~~js
musicSdk[musicInfo.source]
  .getMusicUrl(toOldMusicInfo(musicInfo), targetQuality).promise
~~~

QQ SDK 再将歌曲对象和音质交给当前选择的 API 实现。用户 API 由 api-source.js 的 userApi.apis[source] 分发。播放 URL 解析不是搜索模块的职责。

### 2.6 用户源运行时与 HTTP 桥接

洛雪用户源通过独立的受限 BrowserWindow 执行。其 preload 暴露 `lx` API，`lx.on('request', handler)` 注册处理器，`lx.send('inited', ...)` 声明平台、动作和音质。播放时 preload 调用 handler，并将参数组织为 `{ source, action, info }`；`this` 指向 `lx`。`musicUrl` handler 必须返回 Promise，最终结果必须是 HTTP(S) URL 字符串。

洛雪的 `lx.request(url, options, callback)` 使用 callback(err, response, body)，response 包含 `statusCode`、`statusMessage`、`headers`、`bytes`、`raw`（Buffer）和 `body`（可解析为 JSON 的响应会转换成对象）。options 支持 method、timeout、headers、body、form、formData；默认响应超时 60 秒，最大 60 秒，并返回取消函数。

My-LiveHub 的 Goja 已模拟 request 事件、lx this 上下文、Promise、定时器、Buffer 辅助结构和 HTTP callback，基础方向正确。已确认的差异：
- My-LiveHub HTTP 默认超时 10 秒、上限 30 秒；洛雪默认及最大值为 60 秒。
- My-LiveHub 的 musicUrl 结果解析额外接受 `{url, headers}` 对象；洛雪 preload 只接受 URL 字符串，并由宿主按原始请求结果封装。
- My-LiveHub response 还提供 `status`、`url` 等额外字段；需保持向后兼容，但不应将其误认为洛雪必需字段。
- My-LiveHub 的隔离运行时是否覆盖所有音源用到的 Buffer/crypto/zlib/HTTP 细节，必须用实际脚本验证；不能只靠初始化握手通过来判断。

这些差异应进入协议兼容测试。超时策略需保留 My-LiveHub 的资源保护能力，同时确保不会截断洛雪合法的 60 秒请求；不能无条件取消安全限制。

### 2.5 失败与换源

洛雪首先尝试当前歌曲来源的解析；失败后调用 getOtherSource 搜索其他平台，使用 findMusic 的匹配结果逐个尝试可用 API 和歌曲支持的音质。失败平台会被记录，避免重复尝试。解析成功才把 URL 交给播放器。

播放器还负责处理 URL 获取超时、429 延迟重试、歌曲切换期间的过期请求、自动跳过及播放状态事件。这些属于播放协调器，不应塞入搜索适配器。

## 3. My-LiveHub 当前实现审计

### 3.1 已有资产（保留）

- app/internal/music/player.go：音源注册、搜索接口和歌曲基础模型。
- app/internal/music/queue.go：有界队列、当前歌曲、请求者及历史操作基础。
- app/internal/music/engine.go、mpv_*.go：统一播放引擎与 mpv IPC。
- app/internal/music/lx_user_source_*.go：脚本导入、隔离执行、异步 HTTP 桥接及 URL 校验。
- app/internal/desktop/music_service.go：弹幕命令入口、业务服务、队列协调和播放器生命周期。
- QQ、网易云、酷狗、酷我搜索适配器及其测试。

不推倒重写 mpv，不移除 Goja，不把每个音乐平台拆成独立播放器。

### 3.2 已确认差异与缺陷

| 位置 | 当前行为 | 与洛雪的差异 / 处理 |
|---|---|---|
| music/player.go | Track.SourceData map[string]string 承载平台字段 | 可暂时兼容旧数据，但不能继续作为唯一的平台元数据模型；迁移到有类型、可序列化的 ProviderMetadata |
| music/player.go | 一个 Source 同时要求 Search 和 Resolve 方法 | 搜索和解析器职责耦合；拆为 SearchProvider 与 Resolver 接口 |
| music/player.go | SearchPage 并发聚合、按标题相似度去重排序 | 适合搜索页展示，不等同洛雪跨平台歌曲匹配；两种用途分开 |
| desktop/music_service.go | resolveAlternate 使用严格标题相等、歌手包含关系、时长 10 秒阈值 | 不等同洛雪 findMusic；替换为专用匹配器 |
| desktop/music_service.go | selectLXQuality 固定优先尝试 320k、flac24bit、flac、128k | 没有完整遵循用户音质设置与洛雪 getPlayQuality 的选择规则 |
| desktop/music_service.go | buildLXMusicInfo 添加大量通用别名字段 | 自定义源收到的对象不应由猜测的兼容别名组成；按 toOldMusicInfo 精确构造 |
| desktop/music_service.go | QQ albumId 被构造成数字 album.id | 已确认违背洛雪；必须恢复为字符串 album.mid，并修正错误测试 |
| music/lx_qq.go | QQ 原始 interval、文件大小等保存在字符串 Map | 保留原始数据可以，但需在统一模型转换时精确生成洛雪格式的 interval、types、_types |
| desktop/music_service.go | 每次解析先检查所有导入脚本，再构造扁平对象 | 应保留隔离执行，但需确认脚本注册生命周期、平台声明、音质和请求参数与洛雪 wrapper 一致 |
| desktop/music_service.go | 播放失败后重新搜索并尝试其他来源 | 方向正确，但匹配规则、音质选择、已尝试来源记录和超时重试需与洛雪行为对齐 |

### 3.2.1 四个平台的搜索数据对照

| 平台 | 洛雪实际搜索数据 | My-LiveHub 当前情况 | 结论 |
|---|---|---|---|
| QQ / tx | songId、songmid、albumId、albumMid、strMediaMid、interval、types、_types | LXQQMusicSource 已使用相同桌面搜索接口并保存文件大小；此前 albumId 转换错误已单独修复 | 仍需以 JSON fixture 验证完整旧式对象和音质大小格式 |
| 网易云 / wy | 使用 EAPI /api/search/song/list/page；从 resources[].baseInfo.simpleSongData 提取歌曲、专辑、封面、时长、privilege 和各音质文件大小 | 当前 NetEaseSource 使用旧 /api/search/get/web；保存基本歌曲字段，但没有洛雪的 privilege / 文件质量对象 | 搜索协议和元数据结构均不同，需按洛雪实际接口重做适配，不能把当前 endpoint 当作等价实现 |
| 酷狗 / kg | 保存 Audioid、FileHash、MixSongID、AlbumID、Duration、专辑、歌手；FileSize/HQFileSize/SQFileSize/ResFileSize 对应各音质 hash 和 size；还展开 Grp 子项 | 当前 KugouSource 保存 AudioID、FileHash、标题、歌手、专辑和时长，但未解析音质文件大小/hash、MixSongID、AlbumID，也未展开 Grp | 现有数据不足以生成洛雪 KG musicInfo，且会漏掉分组候选 |
| 酷我 / kw | 从 MUSICRID 提取 songmid；解析 N_MINFO 中的 bitrate/format/size，生成 types/_types；保存 albumId、albumName、格式化 interval | 当前 KuwoSource 仅提取 MUSICRID、标题、歌手、专辑和 DURATION，没有读取 N_MINFO | 缺少每首歌的音质能力，不能正确按音质选择或生成洛雪旧式对象 |

注意：洛雪各平台的音质信息不是统一从一个字段推导。QQ 以 file.size_* 为依据；酷狗以各质量文件大小和 hash 为依据；酷我解析 N_MINFO；网易云以 privilege 和各质量文件对象为依据。必须为每个平台单独实现映射。

### 3.2.2 当前 LX 扁平对象构造的共性问题

洛雪 toOldMusicInfo 对所有在线来源都会提供 albumId、types、_types、interval、albumName、img 和 typeUrl，再按平台补充专属字段。当前 My-LiveHub buildLXMusicInfo 只有 QQ 分支生成 types/_types；其他在线来源没有按各自元数据生成完整质量对象。当前 selectLXQuality 对非 QQ 来源主要依赖音源声明的 qualitys，而没有检查该首歌曲的实际质量能力。这会导致把音源支持的音质误认为歌曲本身支持的音质。

### 3.3 不能提前下结论的部分

- Goja 当前环境是否完整兼容所有洛雪自定义源所用的 Node/Electron API，必须用真实源脚本逐项验证；不能因初始化成功就认定播放 API 兼容。
- LX request HTTP 桥接的响应字段、Buffer、编码、重定向、请求头和异步 Promise 行为，需与洛雪 API wrapper 的真实实现继续逐项核对。
- 各平台音质数据转换不能用 QQ 的规则套用到其他平台。
- 当前野花音源的 no matching track found 必须通过记录真实输入对象、来源 key、音质和脚本发出的请求来定位，不能直接归因于远端曲库。

## 4. 目标架构

~~~text
music/
├── model/             # 统一歌曲模型、平台元数据、音质能力、旧式对象转换
├── search/            # 平台搜索接口、分页、结果标准化
├── matching/          # 洛雪 findMusic 规则及跨平台候选排序
├── resolver/          # 解析器接口、原生解析器、LX 用户源解析器
├── queue/             # 待播队列、当前项、历史
├── playback/          # 播放协调器、重试、换源、mpv 状态同步
└── engine/            # mpv 统一播放器
~~~

以上是目标职责，不要求一次性机械搬移现有文件。先通过兼容适配层迁移，再逐步清理旧接口。

核心接口方向：

- SearchProvider.Search/SearchPage：只负责搜索。
- Resolver.Supports/Resolve：声明支持的 provider key、动作和音质，并返回播放资源。
- Track：通用字段 + ProviderKey + ProviderMetadata（保留真实 JSON 类型）+ QualityCapabilities。
- MusicMatcher：实现洛雪跨平台匹配规则。
- PlaybackCoordinator：只负责播放状态机、解析尝试顺序、重试和播放器调用。
- PlaybackEngine：保留现有 mpv 抽象。

LX 兼容层必须以洛雪 toOldMusicInfo 的输出为唯一数据契约；不要额外注入未经洛雪源码证实的字段。

## 5. 分阶段实施顺序

### 阶段 1：完成协议审计与回归基线
- 补齐洛雪 API wrapper、请求桥接和用户源运行环境的源码对照。
- 对 QQ、网易云、酷狗、酷我逐个核对原始搜索对象、统一对象、旧式对象和音质字段。
- 建立 JSON fixture，断言传入 LX musicUrl 的对象与洛雪旧式对象完全一致。
- 使用真实野花源做受控验证，避免输出脚本凭据和请求中的敏感内容。

### 阶段 2：统一歌曲模型
- 引入有类型的 ProviderMetadata 和 QualityCapabilities。
- 编写各平台旧模型到统一模型的转换器。
- 编写统一模型到 LX 旧式歌曲对象的转换器。
- 兼容旧队列数据的反序列化，避免升级丢失待播歌曲。

### 阶段 3：搜索与匹配
- 拆分 SearchProvider 与 Resolver。
- 保留搜索页多平台聚合。
- 单独实现洛雪 findMusic 匹配流程，作为播放失败后的跨平台匹配器。
- 逐个平台对齐搜索字段、分页、过滤和音质。

### 阶段 4：LX 自定义源兼容
- 按洛雪真实 wrapper 适配 source/action/info/musicInfo/type。
- 核对 HTTP、Buffer、Promise、定时器和运行时 API。
- 让源脚本初始化一次并缓存声明结果，避免每首歌曲都重复执行初始化（前提是隔离和资源生命周期验证通过）。
- 加入真实源协议回归测试。

### 阶段 5：播放协调器
- 按用户音质偏好和单曲音质能力选取音质。
- 当前来源解析失败后，使用洛雪匹配规则寻找候选来源。
- 每个来源最多尝试一次；区分解析失败、资源加载失败和播放失败。
- 保留当前队列项供重试；只有确认播放结束才自动推进队列。

### 阶段 6：完整验收
- 弹幕点歌 → 搜索 → 选歌 → 入队 → 选音质 → LX JS 解析 → mpv 加载 → 播放 → 自动下一首。
- 覆盖无结果、单平台失败、所有解析器失败、HTTP 429、超时、换歌竞态、暂停/恢复、停止和退出。
- 本地 Go 测试、Windows 构建及 GitHub Actions CI 全部通过后再报告完成。

## 6. 实施约束

1. 继续在现有 dev 分支，不创建新分支，不合并 master。
2. 每个阶段保持可构建、可回归；不一次性推倒 mpv 和队列。
3. 不根据字段名称猜契约；必须能指向洛雪对应源码或 fixture。
4. 不把搜索成功当作播放成功；不伪造可播放 URL。
5. 不将会员、地区或 DRM 限制作为绕过目标。
6. 每个运行时改动都增加对应测试；CI 结果必须以 GitHub 实际结论为准。
