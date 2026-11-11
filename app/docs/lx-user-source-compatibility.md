# 洛雪音乐自定义音源兼容

## 已核对的 LX 脚本协议

LX 自定义源不是只包含播放 URL 的 JSON 配置。脚本通过 lx 运行时对象完成初始化和请求：

- 文件头注释提供 @name、@description、@author、@homepage、@version；字段长度与 LX 桌面端一致。
- 脚本执行后调用 `lx.send(lx.EVENT_NAMES.inited, { sources })` 声明支持的平台、动作和音质。
- 通过 `lx.on(lx.EVENT_NAMES.request, handler)` 注册请求处理器；handler 返回 Promise。
- 播放解析请求的数据形状为 `{ source, action: 'musicUrl', info: { type, musicInfo } }`，返回值必须是长度不超过 2048 的 HTTP(S) URL。
- `lx.request(url, options, callback)` 是异步 HTTP 请求接口，options 包含 method、timeout、headers、body、form、formData；回调参数为 error、response、body。超时最大 60 秒。
- `lyric` 和 `pic` 是可选动作。LX 内置支持的平台标识包含 kw、kg、tx、wy 等；对应 My-LiveHub 平台映射为 kuwo、kugou、qq_music、netease。
- 部分脚本还依赖 lx.utils.crypto、lx.utils.buffer、lx.utils.zlib；兼容层必须明确实现这些能力，不能只模拟 send/on。

## 当前已实现

- 本地 .js 脚本导入与文件头元数据识别。
- HTTP(S) URL 下载导入，限制脚本大小、请求超时和重定向次数。
- URL 导入拒绝本机/内网地址，并在实际拨号时校验 DNS 解析结果，防止简单的内网地址访问。
- 本地保存脚本、元数据和 SHA-256 指纹；支持列出和删除。
- 源脚本导入后不会自动执行，UI 明确标注“尚未启用解析”。

## 安全实现要求与剩余工作

LX 官方桌面端把源脚本放在独立 BrowserWindow 中运行，Node integration 关闭，并通过 preload 提供受控请求、加密、Buffer 和压缩能力。该运行环境与 Wails/Go 不同，不能直接把脚本放进主进程执行。

My-LiveHub 下一阶段必须使用独立 JS worker，并且：
- 不暴露 process、require、文件系统、子进程、应用对象或任意 Go 对象。
- HTTP 请求经受控桥接，限制协议、目标地址、响应体、超时和并发；禁止访问本机/内网地址。
- JS 执行设置硬超时、内存/脚本大小限制，失败隔离并可停止。
- 兼容 lx.send/on/request 的 Promise 与回调语义，以及脚本使用到的 crypto、buffer、zlib 子集。
- 每次解析校验返回值为长度受限的 HTTP(S) URL；最终由 mpv 负责实际解码播放。
- 用户导入的脚本默认标记为第三方未验证来源。

**重要：目前已经可以导入、下载、保存和删除脚本，但尚未执行脚本，也尚未声明支持任何平台或音质。导入成功不等于该源已经能解析歌曲。**
