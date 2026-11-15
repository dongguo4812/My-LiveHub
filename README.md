# My-LiveHub

My-LiveHub 是面向 Windows 本地桌面环境的直播互动辅助平台。项目以 Go 为核心，使用 Wails v3 承载桌面窗口，前端采用 Vue 3、TypeScript、Vite 和 Element Plus。平台将直播接入、互动事件、功能模块、媒体播放与 OBS 展示整合到一个本地应用中。

> 当前项目持续开发中。功能以 `app/` 中的实际实现为准；架构设计文档中的规划项不代表已经交付。

## 功能概览

当前客户端包含以下业务页面和能力入口：

- **仪表盘**：查看应用整体运行状态。
- **直播平台**：管理直播平台连接及相关配置。
- **弹幕与互动事件**：查看弹幕、统一互动事件及事件处理情况。
- **互动规则**：配置互动触发规则。
- **用户管理**：查看和管理互动用户信息。
- **事件模拟器**：发送模拟事件，用于功能调试和验证。
- **模块管理**：查看模块状态并执行支持的模块启停操作。
- **扩展管理**：管理扩展及其运行状态。
- **音乐播放**：点歌、搜索音源、播放队列、播放控制及歌词展示；支持洛雪音乐自定义音源脚本接入。
- **视频播放**：视频点播、队列和播放控制。
- **语音播报**：语音相关配置与控制。
- **Live2D**：模型展示及相关控制。
- **LOL Hex Display**：通过扩展提供战绩等直播展示内容。
- **综合展示与布局**：管理 OBS 浏览器展示布局及展示组件。
- **日志诊断与实时监控**：查看运行日志、诊断信息和系统运行状态。

各模块的可用能力取决于当前配置、外部依赖及模块运行状态。外部播放器、直播平台客户端、OBS 等软件需按对应模块要求单独安装和配置。

## 技术栈

| 层次 | 技术 |
|---|---|
| 桌面宿主 | Wails v3 |
| 核心服务 | Go 1.27 |
| 前端框架 | Vue 3 + TypeScript |
| 前端构建 | Vite |
| UI 组件 | Element Plus |
| 路由 | Vue Router |
| 桌面 Web 运行环境 | Windows WebView2 |

## 仓库结构

```text
My-LiveHub/
├── .github/
│   └── workflows/             # GitHub Actions CI
├── docs/                      # 架构及专项设计文档
│   ├── ARCHITECTURE.md
│   ├── MEDIA_RESOURCE_DESIGN.md
│   └── MUSIC_LX_COMPATIBILITY_AUDIT.md
├── app/                       # 正式桌面应用工程
│   ├── main.go                # 应用入口
│   ├── tray_icon.go           # 系统托盘相关实现
│   ├── config/
│   │   └── default/           # 随程序发布的默认配置
│   ├── internal/              # Go 核心与业务实现
│   ├── frontend/              # Vue 前端
│   │   └── src/
│   │       ├── components/    # 可复用组件
│   │       └── views/         # 业务页面
│   ├── extensions/            # 独立扩展及其资源
│   ├── scripts/               # 开发辅助脚本
│   ├── build/                 # Wails 构建配置及资源
│   ├── go.mod
│   └── Taskfile.yml           # 常用开发任务
└── README.md
```

## 环境要求

主要开发和构建环境：

- Windows 10/11
- Go 1.27.x
- Node.js 26.x 与 npm
- Wails CLI v3.0.0
- Microsoft Edge WebView2 Runtime

CI 在 Windows Runner 上执行前端构建、Go 测试和 Windows 可执行文件编译。开发时请确保 Go、Node.js/npm、Wails CLI 均已安装并加入 PATH。

## 开发启动

在 Windows PowerShell 中执行：

```powershell
# 进入正式应用工程
cd app

# 首次使用时安装前端依赖
npm --prefix frontend install

# 启动桌面开发环境（Wails v3）
wails3 dev -config ./build/config.yml
```

开发启动配置会以 `dev` 参数运行 Go 应用。开发环境默认配置从 `app/config/default/` 读取，本机覆盖配置从 `app/config/local/` 读取。

也可以在 `app/` 目录使用 Task：

```powershell
task dev
```

## 构建与测试

在 `app/` 目录执行：

```powershell
# 前端生产构建
npm --prefix frontend run build

# Go 单元测试
go test ./...

# 构建 Windows EXE
go build -o bin/My-LiveHub.exe .
```

使用 Wails 完整构建：

```powershell
wails3 build
```

对应 Task 任务：

```powershell
task frontend:install
task frontend:build
task test
task build
```

CI 工作流位于 `.github/workflows/desktop-validation.yml`。它会检查 Go module 元数据、生成 Wails TypeScript 绑定、安装前端依赖、构建前端、运行 Go 测试并编译 Windows EXE。

## 配置与运行数据

配置分为随程序发布的默认配置和本机覆盖配置：

- **开发环境**：默认配置位于 `app/config/default/`，本机覆盖配置位于 `app/config/local/`。
- **正式环境**：默认配置由程序内嵌资源提供；本机覆盖配置位于 `%APPDATA%/My-LiveHub/config/local/`。

正式环境不依赖源码目录。日志、缓存、数据库、下载媒体等运行时数据应由应用按各自数据路径管理，不应手动放入源码目录。

请勿将个人配置、访问令牌、Cookie、密钥或其他凭据提交到 Git。需要分享配置示例时，应先移除所有敏感值。

## 外部依赖说明

部分功能需要外部软件或服务配合，例如：

- OBS Studio：用于直播画面与浏览器展示。
- 洛雪音乐：用于洛雪客户端连接及兼容自定义音源脚本的相关功能。
- mpv：音乐播放引擎所需的外部播放器组件。
- Live2D 模型资源及相关运行依赖。
- 各直播平台的账号登录、网络连接及平台侧权限。

这些依赖并非全部随 My-LiveHub 一起分发。请在对应功能的设置页面中检查连接状态、资源路径和运行要求。

## 文档

- [总体架构设计](docs/ARCHITECTURE.md)：项目定位、模块边界、数据与配置原则。
- [多平台媒体资源管理设计](docs/MEDIA_RESOURCE_DESIGN.md)：媒体资源目录、缓存及事件处理设计。
- [正式应用说明](app/README.md)：应用工程的开发入口和运行模式。

## 开发约定

- 日常开发统一在 `dev` 分支进行。
- `master` 用于稳定版本，仅在明确安排时从 `dev` 合并。
- 修改前确认当前实现和调用关系，尽量局部修改并补充回归测试。
- 不将设计文档中的规划描述为已实现功能。
- 涉及外部平台、播放器或 OBS 的功能，应区分代码实现、自动化测试和实际运行验证结果。

## 许可证与第三方组件

项目依赖的第三方组件及其许可信息请查看 [app/THIRD_PARTY_NOTICES.md](app/THIRD_PARTY_NOTICES.md)。
