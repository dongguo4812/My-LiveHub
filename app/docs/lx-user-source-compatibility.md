# 洛雪音乐自定义音源兼容

## 已核对的 LX 脚本协议

LX 自定义源不是只包含播放 URL 的 JSON 配置。脚本通过 lx 运行时对象完成初始化和请求：

- 文件头注释提供 @name、@description、@author、@homepage、@version。
- 脚本执行后调用 lx.send(lx.EVENT_NAMES.inited, { sources }) 声明支持的平台、动作和音质。
- 通过 lx.on(lx.EVENT_NAMES.request, handler) 注册请求处理器。
- 播放解析请求的数据形状为 { source, action: 'musicUrl', info: { type, musicInfo } }，返回值为 HTTP(S) URL。
- lyric 和 pic 是可选动作。

## 安全实现要求

LX 官方桌面端把源脚本放在独立 BrowserWindow 中运行，Node integration 关闭，并通过 preload 提供受控请求、加密、Buffer 和压缩能力。该运行环境与 Wails/Go 不同，不能直接把脚本放进主进程执行。

My-LiveHub 必须使用独立 JS VM/worker，并且：
- 不暴露 process、require、文件系统、子进程、应用对象或任意 Go 对象。
- HTTP 请求经受控桥接，限制协议、响应体、超时和并发；禁止访问本机/内网地址。
- JS 执行设置硬超时、内存/脚本大小限制，失败隔离并可停止。
- 每次解析校验返回值为长度受限的 HTTP(S) URL；最终由 mpv 负责实际解码播放。
- 用户导入的脚本默认标记为第三方未验证来源。

## 当前实现范围

当前提交加入了 LX 文件头元数据解析、脚本大小限制、内容 SHA-256 身份和单元测试。它只负责安全导入前的静态校验，尚未执行脚本，也尚未声明支持任何平台或音质。下一阶段必须完成隔离 JS runtime 和 lx.send/on/request 桥接，之后才能接入播放器；再下一阶段接入本地 .js / URL 导入和源管理界面。
