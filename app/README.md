# My-LiveHub 正式客户端骨架

本目录是正式应用工程；技术验证原型仍保留在 `prototype/desktop-validation`。

## 技术栈

- Wails v3 + Go
- Vue 3 + TypeScript + Vite
- Element Plus
- Vue Router

## 开发

在本目录执行：

```powershell
wails3 dev -config ./build/config.yml
```

开发启动配置会显式执行 `go run . dev`，使用当前工作区的 `config/default/` 和 `config/local/`。

前端依赖首次安装：

```powershell
cd frontend
npm install
```

## 正式启动

正式构建不传入 `dev` 参数。直接启动生成的 EXE 即按正式环境运行：默认配置从 EXE 内嵌资源读取，本机覆盖配置位于 `%APPDATA%/My-LiveHub/config/local/`。

当前阶段仅建立桌面宿主和管理端 UI 外壳，并初始化统一配置管理器。业务服务、数据库、平台适配器、事件总线及 OBS 服务将在后续阶段接入。
