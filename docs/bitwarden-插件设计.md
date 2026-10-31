# Bitwarden 网页保险库插件：设计与集成方案

## 调研结论（2026-10-30）

| 风险点 | 结论 |
|---|---|
| WebCrypto 安全上下文 | ✅ `wails://localhost` 在 WebKitGTK 下 `isSecureContext:true`，`crypto.subtle` 可用（实测） |
| 云 API CORS | ❌ identity/api 均不回 ACAO 头 → 必须代理（实测 curl） |
| WebSocket（notifications/SignalR） | ✅ WS 不受 CORS 约束，直连外部服务器不受我们 asset server 影响 |
| Service Worker | ✅ 宿主已中和 SW 为透传 |
| iframe 嵌入 | ✅ 页面由我们本地伺服，无 X-Frame-Options 问题 |
| 登录态持久化 | ✅ localStorage/IndexedDB 落 WebKit 存储（wails://localhost 源） |
| 构建 | ⚠️ Angular 20 大仓（需 Node 20/22，本机 26 → mise 装 22；磁盘 ~3GB，构建 10-20 分钟） |

## 架构（零 vault 补丁）

```
┌─ webtool 宿主 ─────────────────────────────────────┐
│  iframe: /p/<token>/dist/index.html（本地构建的 web vault）│
│    │ fetch/XHR 到 *.bitwarden.com                  │
│    ▼ （宿主桥注入的包装，改写同源）                  │
│  /p/<token>/__wt__/upstream?url=https://api.…      │
│    ▼ （Go 侧转发，无 CORS；域名白名单）              │
│  api/identity.bitwarden.com（或自建服务器）          │
└────────────────────────────────────────────────────┘
```

- **上游代理端点**（宿主新能力，通用）：`/p/<token>/__wt__/upstream`，
  query 带 url；Go 侧校验目标域（插件 manifest 声明 `upstreamHosts`），
  原样转发方法/头/体，回传响应
- **fetch/XHR 包装**（宿主桥注入）：对 `upstreamHosts` 域名的请求改写到代理端点
- **vault 零改动**：不用改 Bitwarden 源码，正常构建 OSS selfhost 配置即可
- WebSocket 直连不经代理（不受 CORS 约束）
- 已知限制（如实告知）：WebAuthn 硬件安全密钥 2FA 在 iframe 内可能不可用
  （需 allow 属性 + 平台支持），TOTP/邮件 2FA 不受影响

## 任务拆解

1. **宿主上游代理**：pluginapi/mirror 加 `upstream` 端点 + 域名白名单（manifest
   `webtool.upstreamHosts`）+ 宿主桥 fetch/XHR 包装 + 单测
2. **vault 构建**：mise Node 22 → clone → `npx nx build web --configuration=oss-selfhost`
   → dist/web → 插件 zip（manifest：bundle + clipboard/storage/http 权限 +
   upstreamHosts: ["*.bitwarden.com"]）
3. **端到端验证 + 文档**：登录（真实账号）/解锁/密码列表/自动填充之外的
   基本操作 + docs/bitwarden-插件.md
