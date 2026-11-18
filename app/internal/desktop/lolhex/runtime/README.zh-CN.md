# LOL Hex 内置模块运行组件

LOL Hex 已纳入 My-LiveHub 主程序的内置模块管理，不再作为独立扩展部署。

## 页面入口

| 页面 | 主程序托管地址 |
|---|---|
| 管理页 | `/extensions/lol-hex/` |
| 海克斯展示页 | `/extensions/lol-hex/display` |
| 战绩条 | `/extensions/lol-hex/bar` |

服务默认监听本机 `127.0.0.1:8767`。上述 URL 是主程序提供的兼容入口，保留现有前端和 OBS 展示行为。

## 功能

- 识别英雄联盟客户端与 LCU 账号状态
- F1 全局截图、OCR 海克斯识别和索引维护
- 海克斯展示页、战绩条及综合展示图层
- 独立管理页面中的截图、卡片和本地数据管理

OCR 无法可靠识别海克斯名称时，会保留截图供人工查看，不会猜测或生成不确定的名称。

## 内置运行结构

- Go 模块管理器：`app/internal/desktop/lol_hex_service.go`
- 运行组件源码：`app/internal/desktop/lolhex/runtime/`
- 正式版部署位置：`modules/lol_hex_display/`（位于主程序运行目录）
- 默认配置：`app/config/default/lol_hex_display.json`
- 本机配置：`config/local/lol_hex_display.json`

正式版会随主程序发布 Python 打包后的运行组件，不要求用户另外安装 Python。开发环境可使用运行组件目录中的 `.venv\\Scripts\\python.exe`；也可通过 `MY_LIVEHUB_PYTHON` 指定解释器。首次开发环境准备：

```powershell
cd app/internal/desktop/lolhex/runtime
py -3.10 -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
```

在该目录执行 `build.bat` 可生成 `dist/lol_hex_display/`，发布工作流会自动构建并将运行组件放入主程序的 `modules/lol_hex_display/`。

## 本机数据与升级兼容

运行数据位于内置运行组件目录下的 `data/`、`screenshots/` 和 `logs/`。打包运行时，Python 进程日志和数据也可能位于 `dist/lol_hex_display/` 对应子目录。

从旧版本升级时，主程序会将旧 `extensions/lol_hex_display/` 目录中的数据、截图和日志复制到新的内置模块目录；已有目标文件不会被覆盖。旧目录只用于一次性数据迁移，不再用于加载代码或启动 Hex。用户配置仍保存在 `config/local/lol_hex_display.json`，不会因本次迁移重置。

Windows 下 F1 全局截图需要管理员权限，启动时会出现 UAC 提示。用户取消 UAC 时，模块会明确报告启动失败，不会伪报运行成功。
