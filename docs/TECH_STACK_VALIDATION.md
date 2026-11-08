# My-LiveHub 桌面技术栈验证计划

> 文档版本：V0.2
> 状态：最小桌面链路验证通过；完整技术栈仍待后续验证
> 更新日期：2026-11-08

## 1. 验证目标

在建立正式业务项目骨架之前，以最小验证程序确认 Windows 桌面宿主、Web 前端、Go 后端及其通信链路能够正常协作。

本阶段只验证技术可行性，不迁移旧项目业务功能，不提前设计完整扩展 SDK，也不将候选技术栈视为不可更改的最终决定。

## 2. 本次验证环境

| 项目 | 实际环境 |
|---|---|
| 操作系统 | Windows 10 Pro 22H2（Build 19045） |
| CPU / GPU | AMD Ryzen 7 5800H / NVIDIA GeForce RTX 3060 Laptop GPU |
| Go | 1.27.3 windows/amd64 |
| Wails CLI | v3.0.0 |
| Node.js | v26.13.0 |
| npm | 11.20.0 |
| WebView2 Runtime | 149.0.4022.62 |
| Wails 环境检查 | `wails3 doctor`：SUCCESS，无阻断问题 |

说明：本次环境检查结果来自实际开发机。Node.js/npm 版本是本次验证时的版本记录，不代表项目必须锁定该版本。

## 3. 候选技术路线

| 层次 | 候选方案 | 验证重点 |
|---|---|---|
| Windows 桌面宿主 | Wails v3 | 窗口、生命周期、WebView2 集成 |
| 核心服务 | Go | 生命周期、HTTP 服务、事件推送 |
| 客户端前端 | Vue 3 + TypeScript + Vite | 与 Wails 的集成和构建 |
| 客户端 UI 组件库 | Element Plus | 常用复杂控件、主题和布局适配 |
| OBS 展示前端 | Vue 3 + 自定义 CSS | 独立访问、透明背景、布局自由度 |
| OBS 展示入口 | Go 本机 HTTP 服务 | 与桌面窗口解耦、多个独立页面入口 |

管理界面组件库用于桌面客户端的操作界面，不意味着产品是网页管理后台。OBS 展示页面不强制使用 Element Plus。

## 4. 已完成的最小验证

原型位置：`prototype/desktop-validation/My-LiveHub-Desktop-Validation`

使用 Wails v3 内置 `vue` 模板（Vue + TypeScript + Vite），保留模板提供的 `GreetService.Greet` 示例方法和 TypeScript 绑定。

| 验证项目 | 结果 | 实际结果 |
|---|---|---|
| Wails 环境检查 | 通过 | `wails3 doctor` 显示 SUCCESS |
| Go 后端启动 | 通过 | `wails3 dev` 成功启动 |
| Windows 桌面窗口 | 通过 | WebView2 桌面窗口正常显示 |
| Vue 页面渲染 | 通过 | 模板页面正常显示 |
| Vue 调用 Go | 通过 | 输入 `My-LiveHub` 后调用 `GreetService.Greet` |
| Go 返回 Vue | 通过 | 页面显示 `Hello My-LiveHub!` |
| 前端生产构建 | 通过 | `vue-tsc` 和 `vite build --mode production` 成功 |
| Windows EXE 构建 | 通过 | 生成 `bin/my-livehub-desktop-validation.exe`，Go Windows GUI 编译步骤完成 |
| EXE 独立启动 | 通过 | 直接运行生成的 EXE，桌面客户端成功启动 |

### 构建时观察到的警告

执行 `wails3 build` 时出现：

- `uname: executable file not found in $PATH`
- `tail: executable file not found in $PATH`
- `-V系统找不到指定的文件。`

这些提示没有阻止本次前端生产构建、资源生成或 Go EXE 编译，生成的客户端也已成功启动。但警告的具体来源尚未定位，后续应检查模板的 `Taskfile.yml` 及本机命令行环境，再决定是否需要修复。不能仅凭本次构建成功就认定警告已解决。

npm 另有 `minimum-release-age` 未识别配置警告；依赖安装及前端构建均成功，未发现依赖安全漏洞。本次暂不为消除非阻断警告而修改模板依赖配置。

## 5. 尚未验证的项目

本次只完成桌面窗口、Vue 渲染、Go 方法调用和 Windows EXE 的最小闭环。以下项目不能标记为已通过：

### 5.1 桌面窗口与生命周期

- 创建、显示、隐藏和关闭第二个窗口。
- 系统托盘及从托盘恢复主窗口。
- 主窗口关闭、应用退出和后台服务停止之间的行为。
- 重复启动、重复关闭等边界情况。

### 5.2 Go 与前端通信及事件

- Go 主动向前端推送状态变化。
- 高频连续推送、突发推送时的 UI 响应。
- 前端窗口关闭或重新创建后，订阅关系正确释放和恢复。
- 确认业务逻辑不依赖某个窗口长期存在。

### 5.3 OBS 独立展示入口

- Go 启动本机 HTTP 服务。
- 使用独立 URL 打开展示页面，不依赖 Wails 主窗口。
- 在 OBS 浏览器源中加载该 URL。
- 透明背景、页面刷新和多布局独立 URL。
- 桌面主窗口关闭但应用服务继续运行时的行为。

### 5.4 稳定性与资源

- 空载和持续事件推送时的 CPU、内存占用。
- WebView2 页面崩溃或窗口关闭后的恢复能力。
- HTTP 服务、事件推送和窗口生命周期互不意外阻塞。
- 应用退出时 goroutine、监听端口、窗口和子进程正确释放。
- 长时间连续运行和事件延迟基准测试。

## 6. 验证原则

1. 验证程序应尽量小，只包含当前需要验证的技术链路。
2. 不接入真实直播平台，不连接 OBS WebSocket，不迁移音乐、视频、TTS 或 Live2D。
3. 不因验证方便而把 OBS 展示页面绑定到桌面窗口生命周期。
4. 记录实际使用的工具链版本、执行命令、结果和问题复现步骤。
5. 遇到框架限制时，先评估解决方式及维护成本，再决定是否调整技术路线。

## 7. 阶段结论与后续决策

**阶段结论：Wails v3 + Go + Vue 3 + TypeScript 已在目标 Windows 开发机上完成最小桌面闭环验证。** 桌面窗口、Vue 页面、Vue 调用 Go、Go 返回结果、Windows EXE 构建及独立启动均已通过。

这足以支持继续以该组合搭建 My-LiveHub 的基础工程，但不代表完整技术栈验收完成，也不代表 Element Plus、OBS 独立展示服务、多窗口、托盘、扩展进程通信、性能和长期稳定性已经验证。

下一阶段建议先验证并设计 Go 核心与独立扩展进程之间的通信协议，包括传输方式、消息信封、请求响应、事件订阅、权限、超时、心跳和进程退出约定；OBS 展示服务、多窗口和托盘等项目应在正式骨架中按阶段验证。
