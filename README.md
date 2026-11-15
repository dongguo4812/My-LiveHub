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

## 下载与运行环境

My-LiveHub 提供 Windows 安装版和解压版。两种版本的运行环境要求相同，均面向 Windows 10/11 64 位系统。

### 安装版

1. 下载 `My-LiveHub-Setup.exe`。
2. 双击运行安装程序，按提示完成安装。
3. 安装完成后，从桌面快捷方式或开始菜单启动 My-LiveHub。

安装版默认安装到当前用户目录下的 `%LOCALAPPDATA%\Programs\My-LiveHub`，不需要管理员权限。

### 解压版

1. 下载 `My-LiveHub-Portable.zip`。
2. 将 ZIP 文件解压到一个有读写权限的目录。
3. 进入解压后的 `My-LiveHub` 文件夹，双击 `My-LiveHub.exe` 启动。

解压版不需要执行安装程序，但不要只提取 EXE 单文件运行；请保留压缩包中的完整目录结构。建议将程序放在长期使用的固定目录中，避免频繁移动程序目录。

### WebView2 Runtime

My-LiveHub 使用 Wails v3 构建桌面窗口，需要 Microsoft Edge WebView2 Runtime 才能正常显示应用界面。

- Windows 11 通常已预装 WebView2 Runtime。
- Windows 10 如果尚未安装 WebView2 Runtime，需要先安装再启动 My-LiveHub。
- 安装版和解压版都需要该运行环境；当前安装包不会自动安装 WebView2 Runtime。

可从微软官方页面下载并安装 **Evergreen WebView2 Runtime**：

https://developer.microsoft.com/microsoft-edge/webview2/

如果启动时提示缺少 WebView2 Runtime，请安装后重新启动 My-LiveHub。

### 洛雪音乐客户端

音乐点播和洛雪播放功能需要另外安装并运行 **洛雪音乐（LX Music）桌面客户端**。洛雪客户端不包含在 My-LiveHub 安装包或解压包中。

使用前请确认：

1. 已单独安装洛雪音乐桌面客户端。
2. 在洛雪音乐中启用本地 API 服务，使 My-LiveHub 能够通过本机接口连接。
3. API 地址与 My-LiveHub「音乐播放」设置中的地址一致。默认地址为 `http://127.0.0.1:23330`。
4. 如需使用自定义音源，请在洛雪音乐客户端中自行安装和配置对应的音源脚本。

点播歌曲时，如果检测到洛雪 API 尚未响应，My-LiveHub 会尝试通过系统注册的 `lxmusic://` 协议启动洛雪客户端，并等待 API 就绪。请确保系统已正确注册该协议。

### 按需安装的软件与资源

以下项目不是启动 My-LiveHub 的必需条件，仅在使用对应功能时需要：

- **OBS Studio**：需要将直播展示内容、浏览器来源或相关画面接入 OBS 时安装并配置。
- **Live2D 模型资源**：使用 Live2D 展示时，按模块要求准备并配置模型资源。
- **直播平台账号及网络权限**：使用对应平台的直播连接、弹幕和互动功能时，需要完成账号登录并确保网络可用。

以上外部软件和资源不会因为安装 My-LiveHub 而自动安装或提供，请根据实际使用的功能自行准备。

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

正式版通过推送 `v*` 格式的 Git Tag 发布，例如 `v0.1.1`。`.github/workflows/publish-release.yml` 会同时构建 Windows 安装版和解压版。每次 Release 提供 `My-LiveHub-Setup.exe`、`My-LiveHub-Portable.zip`、供应用内更新器使用的 `My-LiveHub-windows-amd64.exe`，以及 `SHA256SUMS`。安装版默认安装到当前用户可写的程序目录；解压版解压后即可运行。正式版的数据统一保存在 EXE 所在目录内：配置位于 `config/local/`，缓存位于 `cache/`，日志位于 `logs/`，登录凭据位于 `secure/`。安装升级和手动更新程序时都必须保留这些运行数据；卸载安装版时也保留用户数据目录。 应用内置更新器可从托盘菜单「检查更新」启动，检查 GitHub 最新正式版，显示版本说明与下载进度，并通过 `SHA256SUMS` 校验主程序后提示重启完成替换。当前更新器只替换主程序 EXE；若某次版本同时更新 LOL Hex 等扩展资源，需要运行新版安装包覆盖安装，或使用新版 Portable ZIP 覆盖程序文件，原有运行数据目录会保留。

## 配置与运行数据

配置分为随程序发布的默认配置和本机覆盖配置：

- **开发环境**：默认配置位于 `app/config/default/`，本机覆盖配置位于 `app/config/local/`。
- **正式环境**：默认配置由程序内嵌资源提供；本机覆盖配置位于程序所在目录的 `config/local/`。缓存、日志和登录凭据也保存在程序目录下对应的 `cache/`、`logs/` 和 `secure/`。

正式环境不依赖源码目录；所有可写运行数据均保存在程序所在目录。安装程序必须将客户端安装到当前用户可写的位置，避免写入权限不足。

请勿将个人配置、访问令牌、Cookie、密钥或其他凭据提交到 Git。需要分享配置示例时，应先移除所有敏感值。

## 外部依赖说明

下载后的运行要求及外部软件配置请参阅[下载与运行环境](#下载与运行环境)。OBS Studio、洛雪音乐客户端、Live2D 模型资源及直播平台账号等，均根据实际使用的功能按需准备。

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
