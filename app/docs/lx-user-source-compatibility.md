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
- 源脚本导入后不会自动执行，需用户主动点击“验证脚本”。
- 已接入独立子进程初始化 worker：子进程使用 Goja 执行脚本，未注入 Node.js 的 process、require、文件系统或应用对象；设置 5 秒执行中断与调用栈深度限制。
- 已实现 LX 初始化握手，并在隔离 worker 中实际调用 request 事件处理器；支持同步返回值和已完成/已拒绝的 Promise。
- 已提供受控的 lx.request 回调接口：仅允许 HTTP(S) 公网地址，限制请求方法、重定向、5 秒请求超时和 2 MB 请求/响应体；请求回调在 worker 内同步完成，以支持常见的 Promise 包装回调模式。
- 音乐播放链路已尝试使用已导入的 LX 脚本：将网易云/QQ/酷狗/酷我搜索结果映射为 wy/tx/kg/kw，调用 user_api/musicUrl 处理器，校验返回播放 URL 后交给 mpv。
- 当前 LX 兼容仍是受限子集：formData、crypto、Buffer、zlib 尚未实现；依赖这些能力的脚本仍会失败。网络桥接是同步回调执行，不等同于浏览器事件循环；复杂的异步定时器/多阶段回调脚本尚未支持。

## 安全实现要求与剩余工作

LX 官方桌面端把源脚本放在独立 BrowserWindow 中运行，Node integration 关闭，并通过 preload 提供受控请求、加密、Buffer 和压缩能力。该运行环境与 Wails/Go 不同，不能直接把脚本放进主进程执行。

My-LiveHub 的 worker 已完成第一阶段，但要支持实际播放仍需：
- 不暴露 process、require、文件系统、子进程、应用对象或任意 Go 对象。
- HTTP 请求经受控桥接，限制协议、目标地址、响应体、超时和并发；禁止访问本机/内网地址。
- 当前 5 秒执行中断、调用栈和脚本大小限制已实现；操作系统级内存/CPU 资源配额尚未实现，不能把子进程隔离描述为完整沙箱。后续需补充 Windows Job Object 等系统级限制并验证强制终止。
- 扩展 lx.request 的 formData 及完整异步事件循环语义，并按真实音源脚本补齐 crypto、buffer、zlib 的必要子集。
- 每次解析校验返回值为长度受限的 HTTP(S) URL；最终由 mpv 负责实际解码播放。
- 用户导入的脚本默认标记为第三方未验证来源。

**重要：当前已经打通 request handler、受限 lx.request、musicUrl 结果校验与 mpv 调用链路，但尚未完成真实第三方音源脚本的兼容性验证，也未在用户 Windows 环境验证免登录歌曲播放。部分脚本依赖 formData、crypto、Buffer、zlib 或完整异步事件循环，仍可能无法运行。导入和初始化验证成功不代表每首歌都能播放。**
