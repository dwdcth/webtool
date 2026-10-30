# Wails 3 调研笔记（webtool 重写参考）

> 调研时间：2026-10-30。依据：wailsapp/wails master 分支源码（当天仍在活跃提交，v3.0.0 stable 已正式发布并并入主分支）+ 仓库内文档（`docs/mpress/content/` 即 v3.wails.io 内容源）。
> 结论先行：**v3 stable 已具备 webtool 重写所需的全部关键能力**——自定义资产路由、服务化绑定、多窗口、托盘、全局快捷键、单实例、内置 kvstore/sqlite、内置更新器。架构上无阻塞项；需要正视的只有 Linux 工具链（GTK4/WebKitGTK 6.0）和 iframe 安全边界两条。

## 1. 版本状态与工具链

- **v3.0.0 stable 已发布**，v3 代码已并入 master 主分支，不再是 alpha/beta 分支。发布节奏：每周小版本 + nightly。
- 要求 **Go 1.26+**；模块路径 `github.com/wailsapp/wails/v3`。
- CLI 与 v2 完全分离：`wails3`（v2 的 `wails` 命令继续独立维护）。

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
wails3 setup      # 环境体检 + 装依赖
wails3 init       # 建项目（模板：base/react/svelte/vanilla/vue 等）
wails3 doctor
wails3 generate bindings   # 静态分析生成前端 TS 绑定（@wailsio/runtime）
```

前端运行时是 npm 包 `@wailsio/runtime`，绑定调用默认走 HTTP fetch 到 `/wails/runtime`。

## 2. 应用入口与 Options

入口（无 NewOptions，直接传结构体值）：

```go
app := application.New(application.Options{
    Name:        "webtool",
    Services:    []application.Service{...},   // 替代 v2 的 Bind
    Assets:      application.AssetOptions{...}, // 见 §4
    KeyBindings: map[string]func(window application.Window){...}, // 应用级快捷键
    SingleInstance: &application.SingleInstanceOptions{
        UniqueID: "com.banshan.webtool",
        OnSecondInstanceLaunch: func(data application.SecondInstanceData) { ... },
    },
})
err := app.Run()
```

Options 定义：`v3/pkg/application/application_options.go`。关键字段：`Services`、`Assets`、`SingleInstance`、`KeyBindings`、`Logger/LogLevel`、`Mac/Windows/Linux` 平台选项、`OnShutdown`、`ErrorHandler`、`PanicHandler`、`RawMessageHandler`。

`*App` 以 manager 模式暴露子系统（`application.go:428` 附近）：`Window`、`Event`、`Menu`、`SystemTray`、`Clipboard`、`Dialog`、`KeyBinding`、`GlobalShortcut`、`Screen`、`Browser`、`Updater`、`Autostart`、`Env`。

## 3. Service 系统（替代 v2 Bind）

```go
// 普通注册：导出方法自动可被前端调用
application.NewService(&MyService{})

// 带 Route：服务实现 http.Handler 时，启动时自动挂到 AssetServer 的该前缀下
application.NewServiceWithOptions(svc, application.ServiceOptions{
    Name:  "plugins",
    Route: "/plugins",   // http.StripPrefix(route, handler) 挂载，优先于静态资源
})

// 可选生命周期接口（结构体按需实现）：
//   ServiceStartup(ctx context.Context, options ServiceOptions) error  // ctx 在关停前取消
//   ServiceShutdown() error                                            // 逆注册序调用
//   ServiceName() string                                               // 可选日志名
```

绑定生成：`wails3 generate bindings` 产出带类型和文档注释的 TS。内置服务（`v3/pkg/services/`）：**kvstore**、**sqlite**、fileserver、log、notifications、dock。

- kvstore：`kvstore.New()` → `Get(key) any / Set(key, value) / Delete / Clear / Load / Save`——插件按 id 隔离的 KV 存储可直接用它或 sqlite。
- fileserver：一个"实现 ServeHTTP + 前端可动态换根目录"的官方范本，写插件资产服务前值得先读。

## 4. AssetServer 定制（本项目最关键）

```go
type AssetOptions struct {
    Handler        http.Handler  // 完全接管静态内容提供
    Middleware     Middleware    // 包在整个链条最外层
    DisableLogging bool
}
type Middleware func(next http.Handler) http.Handler
func ChainMiddleware(middleware ...Middleware) Middleware
func AssetFileServerFS(assets fs.FS) http.Handler
func BundledAssetFileServer(assets fs.FS) http.Handler  // 额外提供 /wails/runtime.js
```

已核实的行为（`v3/internal/assetserver/assetserver.go:43-72`）：

- **用户 Middleware 在最外层**，先于一切内部中间件。调 `next` 走默认处理（静态资源 + 服务路由）；**不调 next 即完全接管响应**——可以从任意地方（zip、内存、磁盘缓存）读内容直接写回。
- 拦截的是应用自身的**所有** HTTP 请求（任意子路径）。承载协议因平台而异：Linux/macOS 为自定义协议 `wails://localhost`，Windows 为 `http://wails.localhost`。
- 每个请求带 `x-wails-window-id` / `x-wails-window-name` 头，可识别来源窗口。
- 两条定制路径可叠加：**Service Route** 管结构化前缀（如 `/plugins/*`、`/web/*`），**Middleware** 管横切逻辑（拦截规则、缓存头、鉴权）。官方示例：`examples/gin-service`（Route 挂载）、`examples/gin-routing`（Middleware 分流）。
- 限制：WebSocket 请求直接返回 501；需要 WS 得另起 net/http 端口 + Transport 定制。

## 5. 子系统清单

| 能力 | 状态 | 用法要点 |
|---|---|---|
| 事件 | ✅ | `app.Event.Emit(name, data...)` / `On(name, cb)` / `OnMultiple` / `Off`；窗口事件 `window.OnWindowEvent`；常量在 `v3/pkg/events`。前后端均可收发 |
| 多窗口 | ✅ 一等公民 | `app.Window.NewWithOptions(WebviewWindowOptions{...})`；选项含 Name/URL/HTML/Frameless/AlwaysOnTop/Hidden/HideOnEscape/KeyBindings 等；方法 80+（SetURL/ExecJS/Reload/OpenDevTools…） |
| 系统托盘 | ✅ | `app.SystemTray.New()` → SetIcon/SetMenu/OnClick/AttachWindow；mac 需 ActivationPolicyAccessory |
| 菜单 | ✅ | `application.NewMenu().Add(label).OnClick(...).SetAccelerator(...)`；应用级/窗口级/HTML 上下文菜单（WML） |
| 剪贴板 | ✅ | `app.Clipboard.SetText(text) bool` / `Text() (string, bool)` |
| 对话框 | ✅ | `app.Dialog.OpenFile()/SaveFile()/Info()/Question()...` |
| 窗口快捷键 | ✅ | Options.KeyBindings / WebviewWindowOptions.KeyBindings，如 "CmdOrCtrl+N"、"F12" |
| **全局快捷键** | ✅ | `app.GlobalShortcut.Register(accelerator, cb) error`；Linux 走 X11 或 freedesktop portal（unihub 这块靠 Electron globalShortcut，v3 有原生对应物，不需要第三方库） |
| 单实例 | ✅ | SingleInstanceOptions，见 §2 |
| kv 存储 | ✅ 内置 | `pkg/services/kvstore`；另有 `pkg/services/sqlite` |
| **自动更新** | ✅ 内置（v2 没有） | `app.Updater.Init(updater.Config{CurrentVersion, Providers, PublicKey, CheckInterval})` → `CheckAndInstall(ctx)`；官方 GitHub provider（`pkg/updater/providers/github`）；Ed25519 签名 fail-closed 验证 + 原生更新窗口。示例 `examples/updater` |
| 开机自启 | ✅ | `app.Autostart` |

## 6. 外部网页加载与"拦截"的真实边界

- **能加载外部 URL**：`WebviewWindowOptions.URL` 和 `window.SetURL(s)` 接受任意绝对 URL（`webview_window.go:540`，官方示例直接开 https://wails.io）。
- **不能在 WebView 层拦截外部请求**：AssetServer 只处理 host 为 `wails.localhost` 的请求（`assetserver_webview.go:50-52`）；加载外部站点后，其网络请求由 WebView 原生栈发出，**完全不经过 Middleware**。v3 没有 OnNavigation/decide-policy 之类的导航拦截钩子。
- **结论：要"打开网页 + 自动缓存 + 可控拦截"，必须自建本地反向代理**——把外部站点代理成本地路径（如 `/web/https/example.com/...`），窗口/iframe 加载本地路径，代理层就获得了完整的请求拦截、改写与磁盘缓存能力。这正是 webtool 需求 3、4 的落点。

## 7. 安全边界（插件承载设计的硬约束）

来自 `v3/release_notes.md` 已知限制，已逐条核实：

1. **所有平台：iframe 内的绑定方法调用走 HTTP 到 `/wails/runtime`，guard 不限制**——官方原话 "Treat any iframe you embed as having access to your bound Go methods"。
2. **Linux：同源子帧（srcdoc / about:blank iframe）可经父帧触达原生消息处理器**；跨源帧、以及**不带 allow-same-origin 的沙箱帧会被拦截**。官方："Do not embed untrusted same-origin content in a Wails window."
3. Android（实验性）：bridge 暴露给所有 frame。

**对本项目的直接推论**：插件/网页工具必须放在 `<iframe sandbox="allow-scripts allow-forms allow-popups allow-modals allow-downloads">`（**永远不加 allow-same-origin、allow-top-navigation**）里承载。这样 iframe 是 opaque origin，物理上无法触达 `/wails/runtime` 绑定；宿主与插件之间唯一通道是 postMessage。附带效应：沙箱 iframe 里 localStorage/剪贴板等浏览器能力被禁，插件要持久化/读剪贴板只能走宿主桥——权限模型因此是"被逼出来的真隔离"，而不是君子协定（unihub 在 Electron 里的做法是 preload 全量暴露 + 主进程按 URL 参数查权限，能力隔离偏弱）。

## 8. Linux 构建要求

- 默认 **GTK4 + WebKitGTK 6.0**：`#cgo pkg-config: gtk4 webkitgtk-6.0`。
  - Arch：`pacman -S base-devel gtk4 webkitgtk-6.0`
  - Ubuntu 24.04+/Debian 13+：`apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`
  - Fedora：`dnf install gcc pkg-config gtk4-devel webkitgtk6.0-devel`
- 遗留 GTK3 + WebKit2GTK 4.1：加 `-tags gtk3`（v3.0.x 可用，**v3.1 移除**）。只有 4.0 的老发行版（Ubuntu 20.04 等）不支持。
- 已知限制：无摄像头/麦克风权限提示（默认放行）；`HideOnFocusLost` 在 Hyprland/Sway/i3 自动禁用；托盘表现依赖 StatusNotifier/DBus。

## 9. 与本项目直接相关的官方示例（v3/examples/）

`gin-service`（Service Route 挂载 HTTP）、`gin-routing`（Middleware 分流）、`gin-example`、`window` / `window-api`（多窗口 + SetURL 外部地址）、`systray-basic` 等 5 个托盘、`updater`、`single-instance` / `single-instance-url-scheme`、`binding`、`services`、`keybindings`、`global-shortcuts`、`events`、`dialogs`、`clipboard`、`notifications`。

## 10. webtool 需求 → v3 落地映射

| webtool 需求 | v3 落地 |
|---|---|
| 插件 zip 静态服务（旧版 FileLoader 的正路） | PluginAssetService 实现 http.Handler，`ServiceOptions.Route: "/plugins"` 挂载，从解压目录读文件 + 路径逃逸防护（§4） |
| 插件能力 API（剪贴板/存储/网络/文件/子进程） | BridgeService 绑定 + 前端 postMessage 中转；存储用内置 kvstore/sqlite 按插件 id 隔离（§3、§5） |
| 打开网页→自动缓存→离线工具 | 本地反代 `/web/...` 路由（Service Route）+ 磁盘缓存 + SingleFile 式快照（§6） |
| 拦截规则不写死 | 规则存 JSON，在反代层和插件网络出口统一执行（§4 Middleware/Route 都拿得到全量请求） |
| 插件 UI 承载 | 沙箱 iframe（§7），宿主 shell 用标签页管理 |
| 全局搜索/快捷键/托盘/更新 | v3 内置 GlobalShortcut / SystemTray / Updater（§5），unihub 对应功能可直接对齐 |
