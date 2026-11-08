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
wails3 dev
```

前端依赖首次安装：

```powershell
cd frontend
npm install
```

当前阶段仅建立桌面宿主和管理端 UI 外壳。业务服务、数据库、平台适配器、事件总线及 OBS 服务将在后续阶段接入。
