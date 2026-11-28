# My-LiveHub

My-LiveHub 是面向 Windows 10/11 64 位系统的本地直播互动与媒体辅助平台。项目以 Go 为核心，使用 Wails v3 提供桌面宿主和前后端桥接，前端采用 Vue 3、TypeScript、Vite 与 Element Plus。它将直播平台接入、互动事件、业务模块、媒体播放和 OBS 浏览器展示整合在一个 Windows 桌面应用中。

当前版本已完成开发。本文以 dev 分支当前实现和正式 Release 为依据，介绍已交付能力、运行方式和开发入口；具体功能仍受平台权限、网络、外部软件、模块配置和运行状态影响。规划中的能力不会作为已交付功能描述。

## 项目当前状态

- **桌面应用**：Go + Wails v3，面向 Windows 10/11 x64。
- **管理前端**：Vue 3 + TypeScript + Vite + Element Plus。
- **直播互动**：平台连接、互动事件接收与分发、规则处理、用户信息和模拟事件工具。
- **音乐系统**：独立 HTMLAudio 播放、音源搜索、点歌队列、播放历史、歌单、歌词和JS 音源兼容运行时。
- **脚本运行时**：嵌入式 QuickJS-NG，不依赖 Node.js 执行音源脚本。
- **媒体与展示**：视频点播、语音播报、Live2D、LOL Hex Display、综合展示布局及 OBS 浏览器来源接入。
- **交付方式**：提供 Windows 安装版和解压版；支持应用内正式版更新流程。

功能入口是否可用取决于对应模块是否启用、是否完成账号与资源配置，以及外部服务是否可连接。

## 功能一览

### 直播与互动

- 管理支持的平台连接和相关账号配置。
- 接收并展示直播弹幕及其他平台互动事件。
- 通过统一事件处理路径向业务模块分发事件。
- 配置互动规则和触发行为。
- 查看互动用户信息。
- 使用事件模拟器构造测试事件，验证规则和下游模块。
- 查看模块状态，并对支持动态启停的模块执行管理操作。

平台支持范围、鉴权方式和事件类型以当前平台适配器实现及客户端配置为准。使用真实直播功能需要相应平台账号、网络访问权限及必要的登录授权。

### 音乐中心

音乐模块由 Go 侧业务服务与浏览器侧音频播放共同组成，实际播放使用应用内 HTMLAudio，不需要启动外部播放器。

当前能力包括：

- 聚合多个音源的歌曲搜索结果，并保留歌曲来源信息。
- 点歌、手动选歌和统一播放队列管理。
- 播放、暂停、继续、切歌、进度与音量控制。
- 播放历史、歌单及歌单播放编排。
- 歌词获取、同步歌词展示、歌词详情和独立歌词展示层。
- 按播放资源情况使用媒体代理，支持相应请求头、Range 请求及响度处理通路。
- 导入、启用、停用和管理兼容的 JS 音源脚本。

**播放与音源的边界：** 音源负责搜索和解析播放资源；MusicService 负责队列、历史、歌单及播放生命周期；前端 HTMLAudio 负责实际播放。播放地址解析成功并不保证上游媒体一定可用，播放失败或资源过期仍需由播放流程处理。

#### 音源与 QuickJS-NG

-  JS 音源运行在嵌入式 QuickJS-NG 原生运行时中。
- 不需要洛雪安装或启动音乐桌面客户端，也不通过 Node.js、V8 或外部浏览器执行脚本。
- 生产运行时由 Go 宿主注册受控 API，并管理脚本初始化、Action 调度、超时和资源释放。
- 内置搜索适配器覆盖 QQ、酷我、酷狗、网易云和咪咕等平台；导入脚本可按其声明能力提供或扩展相应 Action。
- 脚本混淆处理使用持久化缓存；缓存与脚本源码摘要及缓存版本关联，命中后仍保留必要的校验步骤。
- 脚本 HTTP 请求通过宿主受控网络能力执行，生产路径对目标地址和连接地址实施安全校验。

“兼容洛雪音源”指兼容相应 JS 脚本格式与 API 约定，不代表依赖洛雪桌面客户端，也不意味着每个第三方脚本的所有 Action 都能在本项目中使用。脚本能力以导入后的识别结果和实际测试为准。

### 视频、语音与展示

- **视频播放**：视频点播请求、播放队列及播放状态管理。
- **语音播报**：语音相关配置、请求与播放控制。
- **Live2D**：模型展示及相关控制，需按模块要求准备模型资源。
- **LOL Hex Display**：内置海克斯识别、战绩展示和相关综合展示组件。
- **综合展示与布局**：管理展示布局及其组件，可通过 OBS 浏览器来源呈现。
- **扩展管理**：管理应用支持的扩展及其运行状态。
- **日志与监控**：查看运行日志、诊断信息和系统运行状态。

各模块的具体功能以客户端页面和当前实现为准。OBS Studio、Live2D 模型等属于按需准备的外部软件或资源，不会随应用自动安装。

## 技术架构

| 层次 | 技术 / 实现 | 主要职责 |
|---|---|---|
| 桌面宿主 | Go、Wails v3 | 应用入口、窗口生命周期、服务注册、前后端桥接 |
| 前端 | Vue 3、TypeScript、Vue Router、Element Plus、Vite | 管理页面、播放器 UI、歌词与展示组件 |
| 业务服务 | Go `app/internal` | 配置、平台接入、事件、语音、音乐、媒体资源和桌面业务 |
| 音源脚本运行时 | QuickJS-NG 原生运行时 | 在受控宿主 API 下执行 JS 音源 |
| 音乐播放 | 浏览器 HTMLAudio / Web Audio | 实际音频播放、播放状态和响度处理 |
| 桌面 Web 运行环境 | Windows WebView2 Runtime | 承载 Wails 前端界面 |
| 本地数据 | JSON 配置与文件系统 | 默认配置覆盖、脚本数据、缓存、日志和凭据 |

My-LiveHub 不是 Electron + Node.js 桌面应用。QuickJS-NG 是嵌入式脚本运行时，不是音频播放器。音乐实际播放由应用内 HTMLAudio 完成，不通过隐藏 mpv 进程播放。

## 下载与运行环境

My-LiveHub 面向 Windows 10/11 64 位系统，提供安装版和解压版。请从项目 GitHub Releases 页面获取正式发行包：

[GitHub Releases](https://github.com/dongguo4812/My-LiveHub/releases)

### 安装版

1. 下载 `My-LiveHub-Setup.exe`。
2. 双击运行安装程序并按提示完成安装。
3. 从桌面快捷方式或开始菜单启动 My-LiveHub。

安装版默认安装到当前用户目录下的 `%LOCALAPPDATA%\\Programs\\My-LiveHub`，无需管理员权限。

### 解压版

1. 下载 `My-LiveHub-Portable.zip`。
2. 将压缩包完整解压到有读写权限的目录。
3. 进入解压后的 `My-LiveHub` 文件夹，运行 `My-LiveHub.exe`。

解压版不需要安装程序。请保留压缩包中的完整目录结构，不要只提取 EXE 单文件运行。建议将程序放在长期使用的固定目录中。

### WebView2 Runtime

Wails v3 使用 Microsoft Edge WebView2 Runtime 显示桌面界面：

- Windows 11 通常已预装 WebView2 Runtime。
- Windows 10 若尚未安装，需要先安装 WebView2 Runtime。
- 安装版和解压版均需要该运行环境；安装包当前不会自动安装 WebView2 Runtime。

微软官方下载页面：[Evergreen WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)

### 按需准备的外部软件与资源

以下内容不是启动 My-LiveHub 的统一前置条件，仅在使用对应功能时需要：

- **OBS Studio**：需要将浏览器展示页面或相关画面接入 OBS 时安装并配置。
- **Live2D 模型资源**：使用 Live2D 展示时，按模块要求准备并配置模型。
- **直播平台账号与网络权限**：使用对应平台连接、弹幕和互动能力时完成登录授权并确保网络可用。

## 应用更新与用户数据

正式版通过 `v*` 格式的 Git Tag 发布。Release 工作流构建 Windows 安装版和解压版，并提供：

- `My-LiveHub-Setup.exe`：Windows 安装包。
- `My-LiveHub-Portable.zip`：完整解压包。
- `My-LiveHub-windows-amd64.exe`：供应用内更新器使用的 Windows 更新程序。
- `SHA256SUMS`：发行文件校验和。

应用内更新器可从系统托盘菜单的“检查更新”入口启动，检查 GitHub 最新正式版、展示版本说明和下载进度，并通过 `SHA256SUMS` 校验更新文件。更新完成后按提示重启应用以完成替换。

正式版可写数据保存在 EXE 所在目录：

| 数据 | 路径 |
|---|---|
| 本机配置 | `config/local/` |
| 缓存 | `cache/` |
| 日志 | `logs/` |
| 登录凭据 | `secure/` |

更新或手动替换程序时，请保留上述运行数据目录。卸载安装版也会保留用户数据目录；如需彻底清除数据，请在确认备份后自行处理对应目录。

## 配置与安全

配置分为随程序发布的默认配置和本机覆盖配置：

- **开发环境**：默认配置位于 `app/config/default/`，本机覆盖配置位于 `app/config/local/`。
- **正式环境**：默认配置由程序内嵌资源提供，本机覆盖配置位于程序所在目录的 `config/local/`。

正式运行不依赖源码目录。日志、缓存和登录凭据分别保存在程序目录下的 `logs/`、`cache/` 和 `secure/`。

安全与数据注意事项：

- 不要将个人配置、Cookie、访问令牌、密钥或其他凭据提交到 Git。
- 分享配置文件或日志前，先移除账号标识、令牌、Cookie 和其他敏感信息。
- 用户导入的音源脚本在受控 QuickJS Runtime 中执行；脚本网络请求由宿主网络能力管理。
- 本地运行数据和音源脚本属于用户数据，升级应用时不要误删。

## 开发环境

当前应用工程位于 `app/`。主要开发环境：

- Windows 10/11 x64
- Go 1.27.x
- Node.js 26.x 与 npm
- Wails v3.1.0
- Microsoft Edge WebView2 Runtime

在 Windows PowerShell 中执行（需要已安装 Go、Node.js、Wails v3 和 [Task](https://taskfile.dev/)）：

```powershell
cd app

# 首次安装前端依赖
task frontend:install

# 启动开发环境（自动准备 QuickJS-NG 运行时、生成 Windows 资源并执行端口预检）
task dev
```

请优先使用 `task dev`，不要绕过项目脚本直接启动 `wails3 dev`。开发启动会显式传入 `dev` 环境参数，前端端口默认为 `8769`；默认配置从 `app/config/default/` 读取，本机覆盖配置从 `app/config/local/` 读取。

## 构建与测试

在 `app/` 目录执行：

```powershell
# 前端类型检查与生产构建
task frontend:build

# Go 测试
task test

# 准备 QuickJS-NG 运行时并构建 Windows 可执行文件
task build
```

项目构建通过 `app/Taskfile.yml` 和 `app/scripts/lxproduction.ps1` 统一准备 QuickJS-NG 运行时、Windows 应用资源及生产构建标签；日常开发和构建请使用上述 Task 入口，避免绕过必要的准备步骤。

生产 QuickJS-NG 运行时验证需要匹配的原生运行时构建环境；普通 Go 测试不能单独替代生产构建标签下的运行时验证。

### 持续集成

CI 工作流位于 `.github/workflows/desktop-validation.yml`。当前验证范围包括：

- Hex / Python 生命周期脚本语法和检查；
- 前端 TypeScript 类型检查与 Vite 生产构建；
- Go 测试；
- QuickJS-NG 生产运行时构建和生产标签测试；
- 脚本搜索、播放资源和歌词相关集成验证；
- HTMLAudio 浏览器播放及歌词行为测试；
- Windows 应用资源嵌入与可执行文件构建。

外部平台服务和真实直播环境可能受账号、网络及上游服务影响。CI 自动化通过不等于所有第三方平台在任意用户环境下始终可用。

## 仓库结构

```text
My-LiveHub/
├── .github/
│   └── workflows/                  # CI 与 Release 工作流
├── docs/                           # 架构及专项设计文档
│   └── ARCHITECTURE.md
├── app/                            # Windows 桌面应用工程
│   ├── main.go                     # 应用入口
│   ├── config/
│   │   ├── default/                # 默认配置
│   │   └── local/                  # 开发环境本机覆盖配置
│   ├── internal/                   # Go 核心与业务实现
│   │   ├── desktop/                # Wails 服务与桌面桥接
│   │   ├── music/                  # 音乐领域及音源服务
│   │   └── ...
│   ├── frontend/                   # Vue 前端
│   │   └── src/
│   │       ├── components/         # 可复用组件
│   │       ├── services/           # 前端服务
│   │       └── views/              # 业务页面
│   ├── scripts/                    # 开发与验证脚本
│   ├── build/                      # Wails 构建配置及资源
│   ├── go.mod
│   └── Taskfile.yml
├── README.md
└── ...
```

## 项目文档

- [总体架构](docs/ARCHITECTURE.md)：项目定位、模块边界、服务生命周期、音乐与 QuickJS 运行时架构。
- [应用工程说明](app/README.md)：应用工程开发环境、构建和配置路径。
- [第三方许可证文件](app/third_party/licenses/)；[Live2D Runtime 第三方声明](app/frontend/public/live2d-runtime/THIRD-PARTY-NOTICES.md)。

## 开发约定

- 日常开发以 `dev` 分支为集成分支；稳定版本按项目发布流程从已验收代码发布。
- 修改前检查当前实现和调用关系，尽量保持修改范围清晰，并补充必要的回归测试。
- 区分已实现功能、外部依赖能力和规划事项，不将设计意图描述为已交付行为。
- 涉及平台、播放器或 OBS 的变更，应区分代码实现、自动化测试与真实环境验证。
- 不提交本机配置、运行缓存、用户音源脚本或任何敏感凭据。
