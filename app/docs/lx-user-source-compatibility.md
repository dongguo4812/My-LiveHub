# 洛雪音乐自定义音源：第一阶段基础兼容

## 目标与范围

本阶段以洛雪音乐桌面版的自定义源协议和运行方式为行为参考，先提供可导入、可初始化、可持续运行、可接收宿主请求的基础运行环境，让搜索和播放业务可以继续接入。这里的“基础兼容”不是完整 Chromium，也不表示所有洛雪脚本都已验证通过；细节差异和少见 API 放到后续兼容优化阶段。

## 已实现的基础能力

### 导入与管理

- 导入本地 JavaScript 脚本和 HTTP(S) URL 脚本。
- 读取文件头 `@name`、`@description`、`@author`、`@homepage`、`@version` 元数据，并按洛雪的字段长度规则截断。
- 检测相同脚本内容的重复导入。
- 本地持久化脚本、元数据、SHA-256 指纹、来源 URL、更新时间提醒设置。
- 支持列表、删除、URL 更新、设置活动源和停用活动源。
- 激活新音源时先读取并启动新运行实例，启动成功后才切换活动 ID 和关闭旧实例；失败时保留旧音源。

### JavaScript 运行环境

- 使用 Goja 执行 JavaScript，并通过独立、长期运行的子进程隔离每个活动音源。
- 不向脚本暴露 Node.js 的 `process`、`require`、文件系统、子进程或 My-LiveHub 应用对象。
- 提供 `lx.env`、`lx.version`、`lx.currentScriptInfo`、`lx.EVENT_NAMES`、`console`、`window`、`self` 等基础运行时对象。
- 支持 `lx.send(lx.EVENT_NAMES.inited, ...)` 初始化握手和 `lx.on(lx.EVENT_NAMES.request, handler)` 请求事件。
- 一个活动源复用同一个 JavaScript VM，保留全局变量和闭包状态；切换、停用或应用关闭时释放进程。
- 支持同步结果、Promise、微任务队列、`setTimeout`、`setInterval` 及清理函数。
- 提供 `atob`、`btoa`、`queueMicrotask` 等常用浏览器基础函数。当前不提供完整 DOM、Chromium 页面环境或通用 `fetch`。

### LX 工具与 HTTP

- `lx.request(url, options, callback)` 通过宿主 HTTP 桥接执行，支持常用 method、headers、body、form、formData、JSON 和回调取消。
- 默认请求响应头等待超时为 60 秒，连接和 TLS 握手超时为 10 秒；不自动跟随 HTTP 重定向，以匹配当前洛雪/Needle 请求行为。
- 请求体和响应体各限制为 2 MB；宿主请求事件数据限制为 64 KB；脚本大小限制为 9 MB。
- HTTP(S) 地址校验保留洛雪桌面端允许访问局域网服务的行为。导入和请求都不应被误描述为“仅允许公网地址”。
- `lx.utils.buffer` 提供常见 Buffer 创建、编码转换、切片、比较和查找能力。
- `lx.utils.crypto` 提供 MD5、随机字节、AES 常见模式和 RSA 无填充加密。
- `lx.utils.zlib` 提供 Promise 形式的 inflate/deflate。
- 支持 `musicUrl`、`lyric`、`pic` 等宿主动作的基础结果结构和 URL/文本长度校验。

## 本阶段验收范围

1. 本地脚本和 URL 脚本可以导入、列出、更新和删除。
2. 脚本能够完成初始化握手，宿主能读取声明的平台、动作和音质。
3. 活动源在多个请求之间保持同一 JS 运行状态。
4. 宿主请求能够按 requestKey 关联响应；超时、取消、脚本异常和进程退出不会把响应交给错误请求。
5. 常用 HTTP、Buffer、crypto、zlib、Promise 和定时器功能有自动化测试覆盖。
6. 激活失败不会丢失原来正常工作的活动源；停用、删除和应用关闭会释放运行实例。

## 暂不属于第一阶段的工作

- 完整 Chromium DOM、完整 Web API、完整 Node Buffer 和所有 Node 加密算法兼容。
- 所有冷门 HTTP 选项、重定向、代理和浏览器网络行为的逐项一致。
- 操作系统级 CPU/内存配额。独立子进程是隔离边界，但不能称为完整安全沙箱。
- 真实第三方音源脚本的广泛兼容性认证，以及 Windows 用户环境下的逐源播放验收。
- 搜索 SDK、多平台搜索、音质选择和 mpv 播放业务链路；这些是下一阶段工作。
- 响度标准化；按项目约定放在最后实现。

## 仍需后续验证

自动化测试通过只能证明当前测试覆盖的行为。正式宣布与洛雪完全一致之前，仍需用多份真实洛雪音源进行导入、初始化、连续请求、超时取消、歌词/封面和播放解析验证，并逐项补齐真实脚本暴露的差异。
