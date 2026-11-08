# 桌面技术验证原型

本目录用于验证 My-LiveHub 的 Windows 桌面技术路线，不承载正式业务代码。原型使用 Wails v3 内置 Vue + TypeScript + Vite 模板。

## 目录结构

```text
prototype/desktop-validation/
├── README.md
└── My-LiveHub-Desktop-Validation/
    ├── main.go
    ├── greetservice.go
    ├── go.mod
    ├── Taskfile.yml
    └── frontend/
        ├── package.json
        ├── bindings/
        └── src/
```

## 环境要求

- Windows 10/11 x64。
- Go 1.26 或更新版本。
- Node.js 与 npm。
- WebView2 Runtime。
- Wails v3 CLI。

安装并检查：

```powershell
go version
node --version
npm.cmd --version
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
wails3 version
wails3 doctor
```

如果 `wails3` 命令无法识别，请确认 Go 的 bin 目录（通常为 `%USERPROFILE%\go\bin`）已加入 PATH，并重新打开终端。

## 初始化原型

从仓库根目录执行：

```powershell
cd .\prototype\desktop-validation
wails3 init -n My-LiveHub-Desktop-Validation -t vue -mod github.com/dongguo4812/My-LiveHub/prototype/desktop-validation
```

Wails CLI 会在当前目录下创建 `My-LiveHub-Desktop-Validation` 子目录。后续命令均在该项目根目录执行：

```powershell
cd .\My-LiveHub-Desktop-Validation
cd .\frontend
npm.cmd install
cd ..
go mod tidy
wails3 dev
```

停止开发模式后，构建并启动独立 Windows 客户端：

```powershell
wails3 build
.\bin\my-livehub-desktop-validation.exe
```

如果当前 PowerShell 无法直接执行 `npm`，使用 `npm.cmd`，无需为此修改系统执行策略。

## 本次验证记录（2026-11-08）

实际环境与完整结果见 [技术栈验证记录](../../docs/TECH_STACK_VALIDATION.md)。

| 项目 | 状态 | 记录 |
|---|---|---|
| Wails 环境检查 | 通过 | `wails3 doctor`：SUCCESS |
| Wails 主窗口启动 | 通过 | `wails3 dev` 正常显示桌面窗口 |
| Vue 页面渲染 | 通过 | Vue 模板页面正常显示 |
| Go 与前端双向调用 | 通过 | 输入 `My-LiveHub`，页面收到 `Hello My-LiveHub!` |
| Windows EXE 构建 | 通过 | `wails3 build` 生成 EXE |
| EXE 独立启动 | 通过 | 直接运行 EXE，客户端成功启动 |
| 第二窗口生命周期 | 未验证 | 不在本次最小验证范围 |
| 系统托盘 | 未验证 | 不在本次最小验证范围 |
| 独立 OBS HTTP 展示页 | 未验证 | 后续阶段验证 |
| Go 主动事件推送 | 未验证 | 后续阶段验证 |
| 刷新/断连恢复 | 未验证 | 后续阶段验证 |
| 资源占用与长期稳定性 | 未验证 | 后续阶段验证 |

### 已知非阻断警告

本机执行 `wails3 build` 时出现 `uname`、`tail` 命令缺失和 `-V系统找不到指定的文件` 提示，但构建流程继续完成，EXE 可以独立启动。具体来源尚待检查 `Taskfile.yml` 和本机命令行环境，不应将其描述为已修复。

npm 提示项目配置 `minimum-release-age` 不受当前 npm 版本识别；本次依赖安装及生产构建成功。暂不为消除该非阻断警告而修改模板依赖配置。

## 原型边界

- 本原型不接入真实直播平台，不连接真实 OBS WebSocket。
- 不迁移 Bili_Live_Danmaku 的业务代码。
- 不包含正式平台的数据库、扩展管理、统一事件总线或业务模块。
- 不把 OBS 展示页绑定到 Wails 主窗口。
- 本次最小闭环通过后，可以继续搭建正式基础工程；完整技术栈仍需按验证计划分阶段验收。
