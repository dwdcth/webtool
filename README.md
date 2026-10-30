# WebTool

插件化网页工具箱（Wails v3 + Go + Vue 3 + Tailwind）。旧版 Wails2 项目的完全重写：
吸收 [UniHub](https://github.com/t8y2/unihub) 的插件设计与插件生态，并加入网页捕获离线化、
数据驱动的请求拦截两大原生能力。

## 功能

- **插件系统**：兼容 unihub 插件包（zip 根目录 `package.json`，`unihub` 字段），
  同时兼容无清单的旧版 zip（自动推断入口）；按 token 路由服务插件资源，
  能力 API（storage/clipboard/http/fs/system/node）在 Go 侧按插件身份强制权限校验
- **插件市场**：内置 unihub 官方源（jsDelivr + 腾讯云 COS），一键安装
- **网页捕获**：把在线工具固化为离线插件——录制模式边用边缓存，
  📌 固化后断网可用；HTML 同源绝对路径自动改写，动态请求靠 Referer 回填兜底
- **拦截规则**：`~/.config/webtool/rules.json` 数据驱动（respond/block/rewrite/pass…），
  界面编辑、保存热生效；旧版硬编码的 ctool `chrome_tool` 拦截已迁移为默认规则
- 浅色/深色主题（默认浅色，记忆偏好）

## 构建与运行

依赖：Go 1.26+、Node/pnpm、GTK4 + WebKitGTK 6.0（Arch: `pacman -S gtk4 webkitgtk-6.0`）。
仅有 WebKit2GTK 4.1 的环境（如本仓库开发机当前状态）加 `-tags gtk3` 过渡：

```bash
pnpm --dir frontend install && pnpm --dir frontend build   # 前端
wails3 generate bindings -d frontend/bindings -ts          # TS bindings（改了 Go 服务后）
go build -tags gtk3 -o build/webtool .                     # 应用
go test -tags gtk3 ./internal/...                          # 测试
```

开发调试：

```bash
go run -tags gtk3 ./cmd/devsetup [zip...]   # 把 zip 装进真实数据目录（默认 ./plugin/*.zip）
go run -tags gtk3 ./cmd/devserve [端口]     # 与应用同构的浏览器 harness（token 路由/拦截规则/镜像）
./build/webtool                             # 启动应用
```

## 数据目录（`~/.config/webtool/`）

```
plugins/          已装插件（解压目录，含 package.json）
plugins.json      安装注册表
plugin-data/      各插件持久化 KV（unihub.storage）
rules.json        全局拦截规则
```

## 插件清单

unihub 兼容 + `webtool` 扩展：

```json
{
  "name": "my-plugin",
  "unihub": {
    "id": "com.example.my-plugin", "name": "我的插件",
    "icon": "🧮", "category": "tool",
    "entry": "dist/index.html", "permissions": ["clipboard", "storage"]
  },
  "webtool": {
    "type": "bundle",
    "interceptors": [
      { "match": { "pathRegex": "^api/telemetry" },
        "action": { "type": "respond", "status": 200, "body": "{}", "contentType": "application/json" } }
    ]
  }
}
```

- `webtool.type`：`bundle`（静态包）| `web`（网页镜像，配 `sourceUrl` + `mode: live|record|offline`）| `link`（在线直连）
- `webtool.sandbox: true` 启用严格沙箱 iframe（默认关闭：WASM/Worker/localStorage 类插件需要同源环境）
- 插件内可用 `window.unihub` / `window.node`（自动注入 `/host/bridge.js`，经 `/p/<token>/__wt__/api/*` 调 Go）

## 安全模型（如实说明）

插件 iframe 默认与宿主同源（与旧版 webtool、unihub 一致）——恶意插件理论上可达宿主绑定。
防线：能力 API 全部经 token 网关 + 权限声明校验；zip 解压与文件服务双重防路径穿越；
service worker 被中和为透传（防缓存劫持）。**只安装可信来源的插件**。

## Roadmap

- [x] P1 插件底座（清单/安装/token 路由/桥/权限）
- [x] P2 网页捕获（镜像代理/录制/固化）+ 拦截规则编辑
- [x] P3 市场页（unihub 默认源一键安装）
- [ ] P3 余项：插件更新检查、dev 模式（devUrl 直载）、sidecar spawn
