# My-LiveHub 应用工程

本目录是 My-LiveHub 正式 Windows 桌面客户端工程。项目整体介绍、功能概览、仓库结构和开发流程请先阅读仓库根目录的 [README.md](../README.md)。

## 技术栈

- Wails v3 + Go
- Vue 3 + TypeScript + Vite
- Element Plus
- Vue Router

## 开发环境

需要 Windows、Go 1.27.x、Node.js 26.x/npm、Wails CLI v3.0.0 和 WebView2 Runtime。

首次安装前端依赖：

```powershell
npm --prefix frontend install
```

启动桌面开发环境：

```powershell
wails3 dev -config ./build/config.yml
```

开发启动配置会显式以 `dev` 模式运行 Go 应用。默认配置从 `config/default/` 读取，本机覆盖配置从 `config/local/` 读取。

## 测试与构建

```powershell
# 前端构建
npm --prefix frontend run build

# Go 测试
go test ./...

# 构建 Windows 可执行文件
go build -o bin/My-LiveHub.exe .

# Wails 完整构建
wails3 build
```

## 配置路径

- 开发环境：默认配置 `config/default/`；本机覆盖配置 `config/local/`。
- 正式环境：默认配置从程序内嵌资源读取；配置位于程序所在目录的 `config/local/`。日志、缓存和登录凭据分别位于程序目录下的 `logs/`、`cache/` 和 `secure/`。

正式运行不依赖源码目录。不要将个人配置、Cookie、令牌或密钥提交到 Git。
