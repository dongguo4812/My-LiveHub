# 桌面技术验证原型

本目录用于验证 My-LiveHub 的 Windows 桌面技术路线，不承载正式业务代码。

## 当前验证目标

- Wails v3 + Go 桌面宿主。
- Vue 3 + TypeScript + Vite 客户端界面。
- 后续增加独立于桌面窗口生命周期的 OBS 展示 HTTP 服务。
- 后续验证 Go 主动事件推送、窗口生命周期、异常恢复和资源释放。

## 环境要求

- Windows 10/11 x64。
- Go 1.26 或更新版本。
- Node.js 与 npm（建议使用当前受支持的 LTS 版本）。
- WebView2 Runtime（Windows 10/11 通常已安装）。
- Wails v3 CLI。

安装并检查：

```powershell
go version
node --version
npm --version

go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
wails3 version
wails3 doctor
```

如果 `wails3` 命令无法识别，请确认 Go 的 bin 目录（通常为 `%USERPROFILE%\go\bin`）已加入 PATH，并重新打开终端。

## 初始化 Wails + Vue + TypeScript

在仓库根目录执行：

```powershell
wails3 init -n desktop-validation -t vue -d prototype/desktop-validation -mod github.com/dongguo4812/My-LiveHub/prototype/desktop-validation
```

Wails v3 内置 `vue` 模板默认使用 TypeScript。初始化后进入项目目录：

```powershell
cd prototype/desktop-validation
npm install
wails3 dev
```

如果初始化命令提示模板或参数不可用，先执行 `wails3 init -l` 和 `wails3 init --help`，不要改用未经审查的第三方模板。

## 验证记录

每次验证请记录 Windows 版本、Go/Wails/Node/WebView2 版本、执行命令、结果和问题复现步骤。

| 项目 | 状态 | 记录 |
|---|---|---|
| 环境检查 | 待验证 | |
| Wails 主窗口启动 | 待验证 | |
| Vue 页面热更新 | 待验证 | |
| Go 与前端双向通信 | 待实现 | |
| 第二窗口生命周期 | 待实现 | |
| 系统托盘 | 待实现 | |
| 独立 OBS HTTP 展示页 | 待实现 | |
| 模拟事件实时推送 | 待实现 | |
| 刷新/断连恢复 | 待实现 | |
| 资源占用与退出释放 | 待验证 | |

## 边界

- 本原型不接入真实直播平台，不连接真实 OBS WebSocket。
- 不迁移 Bili_Live_Danmaku 的业务代码。
- 不把 OBS 展示页绑定到 Wails 主窗口。
- 原型通过验证后，才决定是否将候选技术路线定为正式选型。
