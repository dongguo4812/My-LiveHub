# -*- coding: utf-8 -*-
"""LOL Hex 启动入口。"""
from __future__ import annotations

import json
import os
import sys
from pathlib import Path




def _write_startup_status(state: str, message: str = "") -> None:
    try:
        args = sys.argv
        index = args.index("--startup-status")
        path = Path(args[index + 1])
        temp_path = path.with_suffix(path.suffix + ".tmp")
        path.parent.mkdir(parents=True, exist_ok=True)
        temp_path.write_text(
            json.dumps({"state": state, "message": message}, ensure_ascii=False),
            encoding="utf-8",
        )
        os.replace(temp_path, path)
    except (ValueError, IndexError, OSError):
        pass


def main() -> int:
    import main as hex_main

    if not hex_main.check_admin_privileges():
        result = getattr(hex_main, "ADMIN_LAUNCH_RESULT", "failed")
        if result == "handoff":
            # Do not write a handoff marker: the elevated child may already have
            # reported a startup failure to the same status file.
            return 0
        if result == "cancelled":
            _write_startup_status("failed", "用户取消了管理员权限请求")
            return 1223
        _write_startup_status("failed", "请求管理员权限失败")
        return 1

    try:
        result = int(hex_main.main() or 0)
        if result != 0:
            _write_startup_status("failed", f"Hex 启动入口退出，代码：{result}")
        return result
    except Exception as exc:
        _write_startup_status("failed", f"{type(exc).__name__}: {exc}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
