# LX Music runtime components

This directory vendors the user API runtime components from **LX Music Desktop 2.12.6**.

- Upstream: https://github.com/lyswhut/lx-music-desktop
- Upstream revision: 323e4bac505da428adae8db6b58039fb2b36b86d
- License: Apache-2.0
- Vendored files:
  - `src/main/modules/userApi/renderer/preload.js`
  - `src/main/modules/userApi/rendererEvent/name.js`

The Electron host in this directory is the My-LiveHub/Wails process adapter. The user API preload and event names are taken from the upstream revision above.
