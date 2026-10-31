# webtool 交接文档（Bitwarden 插件排查上下文）

> 2026-10-31 写。给接手的 AI / 开发者：当前项目状态、Bitwarden 集成进展、已知问题与下一步排查方向。
>
> **状态更新（wt9 起）**：本文所述问题已全部修复并验证——环境持久化（localStorage）、
> 白屏/跨域、SignalR 通知、"从不超时"、区域菜单。日常升级流程见
> **`docs/bitwarden-upgrade.md`**（补丁逐项说明 + 重放步骤 + 验收清单），
> 本文仅作排查思路与背景参考。

## 项目概览

**webtool** = Wails v3.0.0 + Go + Vue3 + TypeScript 插件化网页工具箱，gitee `dwdcth/webtool`。
- **本机构建必须带 `-tags gtk3`**（webkit2gtk-4.1；webkitgtk-6.0 未装，装后可去掉 tag）
- 完整构建链：`wails3 generate bindings -d frontend/bindings -ts -f "-tags gtk3"` → `pnpm --dir frontend build` → `go build -tags gtk3 -o build/webtool .`
- 调试工具：`go run -tags gtk3 ./cmd/devserve` → http://127.0.0.1:8899/harness.html（与应用同构的浏览器 harness，共享数据目录）
- 数据目录 `~/.config/webtool/`（插件本体 plugins/、持久化数据 plugin-data/、规则 rules.json、日志 logs/）
- 详见 README.md 和 `docs/wails3存储和剪贴板.md`

## 已完成功能（全部已提交推送）

- P1/P2/P3 全部：插件底座（unihub 兼容清单/token 路由/权限/能力 API）、网页捕获离线化、拦截规则编辑、市场页、更新检查、dev 模式、sidecar spawn
- 内置插件：CTool / PearOCR / Regex101（legacy zip）、时间戳/JWT/Excalidraw/密码管理器（市场）、**Heynote**（全功能：菜单全家桶/粘贴图片/SQLite 存储/markdown 快速识别，见 docs/heynote-upgrade.md）、**Bitwarden**（见下）
- 通用能力：上游代理（fetch/XHR 自动改写 + 域名白名单 + `"*"` 通配 + 同源跳过）、SQLite blob 存储、剪贴板图片（wl-paste/xclip）、插件错误采集落盘

## Bitwarden 集成现状（最新提交 `5d8d81e`）

### 已验证工作

| 项 | 状态 | 验证方式 |
|---|---|---|
| 构建 | ✅ `npx nx build web --configuration=oss`（Angular 21，node@22） | 构建产物 30MB，全相对路径 |
| 登录页渲染 | ✅ 零 JS 错误 | harness 加载 12s 后 DOM 完整 |
| `crypto.subtle` | ✅ `wails://localhost` 是安全上下文 | WebKit Inspector 协议直查 `isSecureContext:true` |
| 上游代理端点 | ✅ `/p/<token>/__wt__/upstream?url=...` 透传方法/头/体 | prelogin 200 + KDF 参数 |
| fetch/XHR 包装 | ✅ 安装且生效 | `fetch.toString()` 非原生；`api.bitwarden.com` 经代理 404（端点不存在但代理链路通） |
| 自托管对话框 | ✅ 共享组件 `SelfHostedEnvConfigDialogComponent` 可打开 | 菜单"自托管…"→ 输入框（HTTPS 校验）出现 |
| 环境指示器 | ✅ 响应式订阅 `environment$`，切换后更新 | harness 中显示"自托管 (服务器主机名)" |
| 同源跳过 | ✅ 本地资源（locales/assets/chunks）不走代理 | harness 日志不再出现重复代理请求 |

### 已知问题（待排查）

#### 问题 1：真实应用中切换服务商后仍可能白屏/跨域

**现象**：用户报告（最近一次）在真实应用（wails://localhost）中切换自托管服务器后白屏、控制台有跨域报错。

**已修的根因**（`5d8d81e`）：`upstreamHosts: ["*"]` 通配把同源请求也改写进代理 → `wails://` URL 被 Go 代理拒绝 → 全页资源挂掉。修复：加 `abs.origin === location.origin` 跳过。

**如果仍白屏**，排查方向：

1. **在真实应用中复现**：启动应用 → 打开 Bitwarden → 切换自托管 → 截图 / 抓错误
   ```bash
   # 抓错误：宿主的插件错误采集（自动落盘）
   tail -f ~/.config/webtool/logs/plugin-errors.log

   # 或用 WebKit 远程 Inspector（最直接）：
   WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:9222 ./build/webtool &
   # 浏览器打开 http://127.0.0.1:9222/Main.html?ws=127.0.0.1:9222/socket/1/1/WebPage
   # 协议直连方式见 docs/wails3存储和剪贴板.md §2.4
   ```

2. **检查 vault 的 API 调用路径**：切换自托管后 vault 会把 API 基址改为用户 URL（如 `https://vault.example.com`）。在 harness 里可以用 `__webtoolErrors` 抓请求失败：
   ```javascript
   // harness 里 iframe 的 contentWindow.__webtoolErrors 数组
   ```

3. **检查 Angular HttpClient 的 XHR 路径**：Angular 默认用 XHR 而非 fetch。我们的包装改了 `XMLHttpRequest.prototype.open`，但 Angular 可能在包装安装前已捕获引用（可能性低但值得排除）。

4. **检查 `setEnvironment` 后的环境重建**：vault 切环境后可能重新初始化一堆 Angular 服务（config、auth 等），触发页面重新加载或路由变化。检查是否有 `window.location.href` 赋值或 `router.navigate` 导致 iframe 导航走。

#### 问题 2：指示器在真实应用中的表现

harness 中指示器正确显示"自托管 (127.0.0.1:8892)"，但在真实应用（wails://localhost）中：
- `location.origin` 是 `wails://localhost`，vault 初始化时判定为 "self-hosted"（因为 hostname 不匹配 bitwarden.com/.eu）
- 这意味着**默认就是自托管模式**，指示器会显示"自托管 (wails://localhost)"直到用户明确设置了服务器
- 这是**正确行为**，但如果用户觉得困惑可以考虑默认显示为空白或不显示

### 关键代码位置

| 文件 | 内容 |
|---|---|
| `frontend/public/host/bridge.js` | 宿主桥：fetch/XHR 代理包装、navigator.clipboard 兜底、unihub API |
| `internal/server/assetservice.go` | `/p/<token>/` 路由 + `__wt__/upstream` 代理端点 + `matchUpstreamHost` |
| `internal/server/upstream_test.go` | 上游代理测试（通配/透传/403） |
| `patches/bitwarden-webapp.patch` | Bitwarden 补丁（162 行）：自托管对话框 + 环境服务解除限制 + 指示器 + 区域切换不导航 |
| `plugin/bitwarden.zip` | 最新插件包（30MB，version 0.0.0-wt3） |

### Bitwarden 补丁文件说明

`patches/bitwarden-webapp.patch` 改了上游 3 个文件（对上游仓库 `bitwarden/clients` 打 `git apply` 即可）：

1. **`apps/web/src/app/components/environment-selector/environment-selector.component.ts + .html`**
   - 登录页区域菜单追加"自托管…"项，点击打开共享对话框
   - 指示器改为订阅 `environment$`（响应式），自托管时显示服务器主机名
   - 引入 `DialogService`、`ToastService`、`SelfHostedEnvConfigDialogComponent`

2. **`apps/web/src/app/platform/web-environment.service.ts`**
   - `setEnvironment(Region.SelfHosted, urls)`: 从 throw 改为保存 + 更新环境
   - 构造尾部：从持久化状态恢复已保存的自托管配置
   - 区域切换不再 `win.location.href = ...` 导航走（改为直接切换 CloudEnvironment）

### 重建 Bitwarden 插件

```bash
# 1. 克隆上游 + 打补丁
cd /tmp && rm -rf bw-clients
git clone --depth=1 https://github.com/bitwarden/clients bw-clients
cd bw-clients && git apply /path/to/webtool/patches/bitwarden-webapp.patch

# 2. 构建（需要 node@22，用 mise）
~/.local/bin/mise install node@22
~/.local/bin/mise exec node@22 -- npm ci
~/.local/bin/mise exec node@22 -- npx nx build web --configuration=oss

# 3. 打包 zip（剔除 .js.map）
# 输出在 dist/apps/web/oss/，打包到 plugin/bitwarden.zip
# manifest 见 /tmp/bw4/package.json（或从现有 zip 里提取）

# 4. 安装
cd /path/to/webtool && go run -tags gtk3 ./cmd/devsetup plugin/bitwarden.zip
```

### 上游代理机制（宿主侧通用能力）

```
插件页面 → fetch/XHR → [宿主桥包装] → /p/<token>/__wt__/upstream?url=<绝对URL>
                                        ↓ Go 侧 matchUpstreamHost 白名单校验
                                        ↓ 通过 → http.Client 转发（60s 超时，不跟随重定向）
                                        ↓ 拒绝 → 403 {"error":"upstream host not allowed"}
```

- manifest 声明：`"webtool": {"upstreamHosts": ["*.bitwarden.com"]}` 或 `["*"]` 全通配
- 同源请求自动跳过（不走代理，避免 wails:// scheme 问题）
- 支持 GET/POST/PUT/DELETE/PATCH，方法/头/体透传

### vault 环境模型（理解关键）

vault 的环境由 `WebEnvironmentService` 决定：
1. 构造时：`envUrls.base = window.location.origin`，按 hostname 匹配已知区域
2. hostname 匹配 bitwarden.com/.eu → 对应 `CloudEnvironment`
3. 不匹配 → `SelfHostedEnvironment`（API 基址 = origin，即 `/api/...`、`/identity/...`）
4. 我们（wails://localhost 或 127.0.0.1:xxxx）永远不匹配 → 默认 SelfHosted
5. 用户通过对话框设置 URL 后 → `setEnvironment(SelfHosted, urls)` → 更新为用户的真实服务器

**关键**：vault 用 Angular HttpClient（默认走 XHR），不走 fetch。所以 XHR 包装是主通道。

## 排查建议（优先级排序）

1. **在真实应用中复现白屏**：用 Inspector 或错误日志抓具体报错，确定是哪个请求/操作触发的
2. **确认 XHR 包装在真实应用中生效**：检查 `XMLHttpRequest.prototype.open` 是否被改写（harness 中 fetch 已验证，XHR 理论上也应生效但未单独验证）
3. **检查 vault 环境切换后的 API 调用**：切换到自托管后，vault 是否正确把 API URL 改为用户服务器（而非继续用 origin）
4. **如果 CORS 仍然出现**：检查具体是哪个请求被 CORS 拦截——如果 URL 是外部域名，说明包装没拦截到；如果是同源路径，说明是其他问题
5. **终极手段**：给 bridge.js 加临时 console.log 打印每个被改写的请求 URL + 未被改写的外部请求 URL，在真实应用中看日志
