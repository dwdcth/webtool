# Wails3 存储与剪贴板：调试经验与插件使用指南

> 本文沉淀 webtool 项目（Wails3 + Go + WebKitGTK/Linux）实战调试得来的经验，
> 以及第三方插件如何使用宿主提供的存储与剪贴板能力。

## 一、剪贴板

### 1.1 各方案的真实能力（Linux/WebKitGTK 实测）

| 方案 | 文本 | 图片 | 实测结论 |
|---|---|---|---|
| Wails3 内置 `app.Clipboard` | ✓ SetText/Text | ✗ 无 API | 只能做文本 |
| `navigator.clipboard.read/readText`（WebView 内） | 理论支持 | 理论支持 | **WebKitGTK 下 NotAllowedError**——无授权 UI，读一律被拒；写 readText 部分可用但不可靠 |
| golang-design/clipboard | ✓ | ✓ | **Linux 上纯靠 xclip/xsel 外部命令**，无 Wayland 原生支持；Wayland 会话且未装 xclip 的机器直接不可用 |
| wl-clipboard（wl-paste/wl-copy） | ✓ | ✓ | Wayland 原生；`wl-paste -t image/png` 直接取图片字节 |

**最终方案（internal/clipboardutil）**：优先 `wl-paste`/`wl-copy`（Wayland 原生），
`xclip`/`xsel` 兜底（X11/XWayland），外加 PNG 魔数校验（防止把错误输出当图片）。
全部 `exec.Command` + 超时控制，无 CGO、无第三方依赖。
依赖：Arch 装 `wl-clipboard`；macOS/Windows 需要时再引 golang-design/clipboard（其跨平台为 CGo）。

### 1.2 关键坑：让现有网页应用无感接入

很多网页应用（如 heynote 粘贴图片）直接调 `navigator.clipboard.read()`。
WebKitGTK 下这一调用直接抛 `NotAllowedError`，**且应用常先 `event.preventDefault()`
把浏览器默认菜单/行为也吞掉**，表现就是"功能整体失效且无报错"。

**解法：宿主桥注入补丁**（`frontend/public/host/bridge.js`）——原生调用失败时回落宿主 API，
返回值伪装成标准 `ClipboardItem` 形状，应用代码零改动：

```js
navigator.clipboard.read = function () {
  return nativeRead().catch(() =>
    call('clipboard', 'read-all', {}).then(d => {
      const items = []
      if (d.dataUrl) items.push({
        types: ['image/png'],
        getType: t => t === 'image/png'
          ? Promise.resolve(dataUrlToBlob(d.dataUrl))
          : Promise.reject(new Error('not found')),
      })
      if (d.text) items.push({ types: ['text/plain'], getType: ... })
      return items
    }))
}
navigator.clipboard.readText = function () { /* 同理回落 */ }
```

### 1.3 图片传输格式约定

跨语言/跨进程传图片用 **dataURL 字符串**（`data:image/png;base64,...`）最省事：
JS 端 `<img src>` 直用、`fetch(dataUrl)` 转 Blob 都方便；Go 端 base64 编解码即可。

### 1.4 一个隐蔽的坑：dataURL 里的分号

heynote 的图片标签格式 `<∞img;id=..;file=..∞>` 用**分号分隔参数**，而 dataURL
自带 `data:image/png;base64,` 的分号——直接内嵌会把参数解析劈断（表现为图片占位符）。
**教训：结构化文本里存图片时，存稳定文件名（如 `wtimg-xxx.png`），渲染时再解析成 dataURL。**

## 二、存储

### 2.1 Wails3/WebKitGTK 下的存储选项对比

| 方案 | 容量 | 落点 | 问题 |
|---|---|---|---|
| localStorage（WebView） | **5-10MB 配额** | `~/.local/share/webtool/localstorage/wails_localhost_0.localstorage` | 大图/多笔记必爆；所有插件共享同一 origin 命名空间 |
| Wails3 kvstore 服务 | 无硬限 | 单全局 JSON 文件 | 全应用一份、JSON 整文件重写、无按插件隔离 |
| 自建每插件 SQLite（我们的方案） | **磁盘级** | `~/.config/webtool/plugin-data/<id>.blob.db` | 无（见下） |

### 2.2 最终架构（modernc.org/sqlite）

- **驱动**：`modernc.org/sqlite` 纯 Go 无 CGO——Wails3 依赖树自带（其 sqlite 服务用的就是它），
  与 `-tags gtk3` 构建完全兼容
- **打开参数**：`file.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`
- **每插件一个库**：`plugin-data/<id>.blob.db`，表 `blobs(key TEXT PRIMARY KEY, data TEXT, created_at)`
- **双层 API**：
  - `unihub.storage.get/set/...` → `<id>.json`（轻量 KV）
  - `unihub.storage.setBlob/getBlob/delBlob/blobKeys` → `<id>.blob.db`（大对象，无配额）
- **键命名约定**（heynote 实例）：`wtbuf:<路径>` 笔记、`wtimg-*.png` 图片、`wtmeta:<名>` 元数据

### 2.3 调试踩过的坑（按血泪程度排序）

1. **beforeunload 覆盖陷阱**：应用退出/页面卸载时会用内存状态兜底保存，
   **运行中写它的存储会被覆盖**。外部改数据必须等应用完全退出后再写
   （`pkill -9` 后 `pgrep` 确认进程消失，注意 pgrep 会匹配到自己的 grep 命令）。
2. **WAL 锁**：应用持有 blob.db 时，外部 sqlite3 可能报 `unable to open database file`；
   排查时先停应用。运行中备份需连 `-wal`/`-shm` 一起拷。
3. **WebView 的 localStorage 是 SQLite 格式且值为 UTF-16LE**：外部读写要用
   `sqlite3` + `value.decode('utf-16-le')`，路径在 `~/.local/share/<应用名>/localstorage/`。
4. **同步渲染 vs 异步读取**：图片渲染（`img.src = ...`）是同步语义，而宿主 API 是异步 fetch。
   解法：**启动时 blobKeys+getBlob 全量预载进内存 Map**，渲染函数查内存，异步只在后台回填。
5. **单一事实源**：localStorage → SQLite 迁移完成后**删除 localStorage 旧键**，
   否则双写漂移（两处状态不一致，后保存者覆盖先迁移者）。

### 2.4 调试工具链备忘

- **插件错误采集**：宿主注入 console.error/warn/unhandledrejection 转发，
  落盘 `~/.config/webtool/logs/plugin-errors.log`（WebKitGTK 没控制台时的眼睛）
- **同构 harness**：`go run -tags gtk3 ./cmd/devserve` → http://127.0.0.1:8899/harness.html
  （与应用同一套 token 路由/能力 API/数据目录，可用浏览器 DevTools 调试）
- **WebKit 远程 Inspector**：`WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:9222 ./应用`，
  浏览器打开 `http://127.0.0.1:9222/Main.html?ws=127.0.0.1:9222/socket/1/1/WebPage`；
  协议直连（绕过 DevTools UI）需先等 `Target.targetCreated` 事件，再经
  `Target.sendMessageToTarget` 包裹 JSON-RPC，回包在 `Target.dispatchMessageFromTarget`

## 三、第三方插件如何使用

插件 HTML 由宿主自动注入 `/host/bridge.js`，直接可用 `window.unihub` / `window.node`。

### 3.1 存储（无需权限声明，按插件自动隔离）

```js
// 轻量 KV（JSON 文件）：设置、主题、计数器等
await window.unihub.storage.set('theme', 'dark')
const theme = await window.unihub.storage.get('theme')
const allKeys = await window.unihub.storage.keys()
await window.unihub.storage.delete('theme')

// 大对象（SQLite，无配额）：图片、文件、整篇笔记
await window.unihub.storage.setBlob('img-001.png', dataUrl)
const dataUrl = await window.unihub.storage.getBlob('img-001.png')  // 不存在返回 ""
await window.unihub.storage.delBlob('img-001.png')
const keys = await window.unihub.storage.blobKeys()  // 全部 blob 键
```

注意：blob 值是字符串——图片用 dataURL，其他大文本直接存原文均可。
**别把几十 KB 以上的东西塞进 `storage.set`**（JSON 整文件重写），走 setBlob。

### 3.2 剪贴板（需在清单声明 `"permissions": ["clipboard"]`）

```js
// 文本
const text = await window.unihub.clipboard.readText()
await window.unihub.clipboard.writeText('hello')

// 文本 + 图片一次拿（图片为 dataURL，无图时为 ""）
const { text, dataUrl } = await (async () => {
  const r = await fetch(location.pathname.match(/^\/p\/[^/]+\//)[0]
    + '__wt__/api/clipboard/read-all', { method: 'POST',
    headers: { 'Content-Type': 'application/json' }, body: '{}' })
  return (await r.json()).data
})()
```

**更推荐：什么都不改。** 宿主已自动补丁 `navigator.clipboard.read/readText`——
原生可用走原生，被拒自动回落宿主（Linux 上读系统剪贴板）。
直接写 `await navigator.clipboard.read()` 的现有网页应用可无改动运行。

### 3.3 环境要求

- Linux/Wayland：`wl-clipboard`（`wl-paste`/`wl-copy`）；X11：`xclip` 或 `xsel`
- 权限模型：`storage` 免声明（天然隔离）；`clipboard` 需声明；
  `fs`/`http`/`spawn` 等高危权限同样在 package.json 的 `unihub.permissions` 声明，
  Go 侧逐方法强制校验
