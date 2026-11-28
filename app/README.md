# My-LiveHub 应用工程

本目录是 My-LiveHub 正式 Windows 桌面客户端工程。项目整体介绍、功能概览、仓库结构和开发流程请先阅读仓库根目录的 [README.md](../README.md)。

## 技术栈

- Wails v3 + Go
- Vue 3 + TypeScript + Vite
- Element Plus
- Vue Router

## 开发环境

需要 Windows 10/11 x64、Go 1.27.x、Node.js 26.x/npm、Wails v3.1.0、Task 及 WebView2 Runtime。

首次安装前端依赖：

```powershell
npm --prefix frontend install
```

启动桌面开发环境（推荐）：

```powershell
task dev
```

`task dev` 会通过 `scripts/lxproduction.ps1` 准备 QuickJS-NG 运行时、生成 Windows 应用资源、执行端口预检并生成前端绑定，然后以 `dev` 环境参数启动 Wails。不要直接使用 `wails3 dev` 绕过这些准备步骤。默认配置从 `config/default/` 读取，本机覆盖配置从 `config/local/` 读取。

## 测试与构建

```powershell
# 前端构建
task frontend:build

# Go 测试
task test

# 准备 QuickJS-NG 运行时并构建 Windows 可执行文件
task build
```

生产构建应使用 `task build`，该任务会设置 `lxproduction` 构建标签并准备兼容的 QuickJS-NG 原生运行时。

## 配置路径

- 开发环境：默认配置 `config/default/`；本机覆盖配置 `config/local/`。
- 正式环境：默认配置从程序内嵌资源读取；配置位于程序所在目录的 `config/local/`。日志、缓存和登录凭据分别位于程序目录下的 `logs/`、`cache/` 和 `secure/`。

正式运行不依赖源码目录。不要将个人配置、Cookie、令牌或密钥提交到 Git。
