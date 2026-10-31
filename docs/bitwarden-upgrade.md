# Bitwarden 插件升级指南

webtool 内置的 Bitwarden 插件基于 [bitwarden/clients](https://github.com/bitwarden/clients)
官方 **web（oss 配置）构建** + 我们的适配补丁打包而成。上游发新版后按本文档重新打包即可。

> 历史排查过程见 `docs/handoff-bitwarden.md`（仅作背景参考，文中"已知问题"均已修复）。

## 组成总览

| 部分 | 位置 | 角色 |
|---|---|---|
| vault 补丁（升级需重放） | `patches/bitwarden-webapp.patch` | 对上游 6 个文件的修改，全部带 `webtool 补丁` 注释 |
| 插件包 | `plugin/bitwarden.zip` | 构建产物 + unihub 清单（package.json），版本号 `0.0.0-wt<N>` |
| 宿主桥 | `frontend/public/host/bridge.js` | 通用（非 Bitwarden 专属）：fetch/XHR 代理改写、WebSocket 隧道改写 |
| 上游代理 | `internal/server/assetservice.go` | `/p/<token>/__wt__/upstream?url=...`，跟随系统代理（clash 等） |
| WebSocket 隧道 | `internal/server/wstunnel.go` | 127.0.0.1 随机端口，为 Bitwarden SignalR 通知而加 |

宿主侧三处随 webtool 本体升级，不在本文档的重放范围；vault 升级只动补丁和 zip。

## 我们改了什么（升级时需要重放的补丁）

补丁以 git diff 形式留档在 **`patches/bitwarden-webapp.patch`**，按重要性排序：

### 1. `apps/web/src/app/platform/web-environment.service.ts`（核心）

本地插件托管（`wails://` 非 https origin）与官方"web 部署即同源"的假设全面冲突，改动最多：

- **构造**：非 https origin 时跳过 hostname 推导（`wails://localhost` 会误匹配 LOCAL
  开发区域，且 Bitwarden 硬校验 API URL 必须 `https://`），默认环境用官方云
  `CloudEnvironment(US)`——注意不能用 `WebCloudEnvironment`（会把 origin 混进 urls）
- **恢复**：订阅原始状态流 `getGlobal(GLOBAL_ENVIRONMENT_KEY).state$`（null=未加载，
  跳过），拿到用户保存的环境后发射并退订。不能订阅派生的 `globalEnvironment$`——
  持久化状态加载前它会先发射推导默认值，曾因此误判
- **setEnvironment(SelfHosted)**：官方直接 throw（假设同源部署），改为保存 urls +
  即时切换 `SelfHostedEnvironment`，配合宿主代理可达
- **setEnvironment(云端区域)**：官方实现是 `win.location.href = 新vault地址` 导航离开，
  改为原地切换 `_environmentSubject.next(new CloudEnvironment(...))`，不导航

### 2. `apps/web/src/app/core/core.module.ts`

`OBSERVABLE_DISK_STORAGE` 由 `window.sessionStorage` 改为 `window.localStorage`。
官方 web 关窗即清隐私优先（环境由域名决定无需持久）；本地插件是单用户桌面场景，
环境选择必须跨启动保留（敏感数据本就经主密码派生密钥加密）。
**不改这个，重启后自托管配置就丢回官方云。**

### 3. `apps/web/src/app/components/environment-selector/`（html + ts）

登录页环境选择器：

- `showRegionSelector = true`：oss 构建 `isSelfHost()` 是编译期常量 true 会隐藏菜单
- 菜单加"自托管…"项，打开共享组件 `SelfHostedEnvConfigDialogComponent`（官方 web
  不给入口，但组件本身就是浏览器扩展/桌面端共用的）
- 指示器改订阅 `environment$` 响应式更新：自托管显示"自托管 (主机名)"；云端区域
  hostname 匹配不上（本地托管必然）时退回当前环境所属区域
- 注意：菜单触发器是 `<a>` 不是 `<button>`（自动化验证时别选错）

### 4. `libs/angular/src/services/jslib-services.module.ts`

`ENV_ADDITIONAL_REGIONS` 的 useValue 过滤掉 `key === "LOCAL"`（构建期 webpack 注入的
上游开发区域 `https://localhost:8080`，本机无服务，且 localhost 本质是自托管的一种，
入口统一走"自托管…"对话框）。菜单因此只剩 bitwarden.com / bitwarden.eu / 自托管…。

### 5. `apps/web/src/app/key-management/session-timeout/services/web-session-timeout-type.service.ts`

`isAvailable(Never)` 由 `isDev()` 改为 `true`：官方 web 因公共电脑安全仅 dev 构建放
"从不超时"；本地插件是单用户桌面场景（与官方浏览器插件/桌面端一致），登录后
设置 → 安全 → 会话超时 下拉可选"从不"。

## 升级步骤

```bash
# 1. 拉取上游新版本（记下 commit，写进提交信息）
cd /tmp && rm -rf bw-clients && git clone --depth=1 https://github.com/bitwarden/clients bw-clients
cd bw-clients && git log --oneline -1

# 2. 打补丁（冲突时按下节处理）
git apply --reject /path/to/webtool/patches/bitwarden-webapp.patch
# 有 .rej 文件时逐个手动合入（每处改动都有 webtool 补丁注释可对照），改完删 .rej/.orig

# 3. 构建（需要 node 22；本机用 mise）
~/.local/bin/mise exec node@22 -- npx nx build web --configuration=oss
# 产物 dist/apps/web/oss，约 4 分钟；结束看 "compiled with N warnings"（有 error 会显式报）

# 4. 打包插件 zip
cd /tmp && rm -rf bw-pkg bitwarden-new.zip && mkdir -p bw-pkg && cd bw-pkg
cp -r /tmp/bw-clients/dist/apps/web/oss dist
find . -name '*.map' -delete                    # sourcemap 不进包
unzip -p <webtool>/plugin/bitwarden.zip package.json > package.json
#    编辑 package.json：version 递增 0.0.0-wt<N+1>（其余 unihub 清单字段不动）
python3 -c "
import zipfile, os
with zipfile.ZipFile('/tmp/bitwarden-new.zip', 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    z.write('package.json')
    for root, dirs, files in os.walk('dist'):
        for f in files:
            z.write(os.path.join(root, f))
"

# 5. 安装到 webtool（必须先清旧记录，devsetup 不覆盖）
python3 -c "
import json, shutil
p = '$HOME/.config/webtool/plugins.json'
d = json.load(open(p))
recs = d.get('plugins', d) if isinstance(d, dict) else d
if isinstance(recs, list):
    d['plugins'] = [r for r in recs if (r.get('id') if isinstance(r, dict) else r) != 'bitwarden']
elif isinstance(recs, dict):
    recs.pop('bitwarden', None)
json.dump(d, open(p, 'w'), ensure_ascii=False, indent=1)
shutil.rmtree('$HOME/.config/webtool/plugins/bitwarden', ignore_errors=True)
"
cd <webtool> && go run -tags gtk3 ./cmd/devsetup /tmp/bitwarden-new.zip
cp /tmp/bitwarden-new.zip plugin/bitwarden.zip   # 留档进仓库

# 6. 更新补丁档案（把最终改动重新 diff 留档）
cd /tmp/bw-clients && git diff > <webtool>/patches/bitwarden-webapp.patch
```

> 用户数据不受升级影响：加密的 vault 数据在服务器端，本地只有经主密码派生密钥
> 加密的状态（localStorage `global_environment_environment` 等）。

## 冲突处理（按概率排序）

| 冲突位置 | 原因 | 处理 |
|---|---|---|
| `web-environment.service.ts` 构造函数 | 上游重构环境初始化（较活跃区域） | 核心不变：非 https origin → 跳过推导 + `CloudEnvironment(US)`；恢复走原始 `state$`（null 跳过）；SelfHosted 不 throw 不导航 |
| `core.module.ts` 存储工厂 | 上游调整 DI | 目标只有一个：`OBSERVABLE_DISK_STORAGE` → `window.localStorage` |
| `environment-selector` 组件 | 上游改模板/组件结构 | 保留三件事：菜单强制显示、自托管入口、指示器响应式；模板类名若变，菜单项仍用 `<a bitMenuItem>` |
| `jslib-services.module.ts` | 上游改 ENV_ADDITIONAL_REGIONS 注入方式 | 任何形式都要滤掉 LOCAL；若上游不再注入则此段可删 |
| `web-session-timeout-type.service.ts` | 上游调整超时类型枚举 | 保持 `Never → true` 即可 |
| 构建配置 | 上游改 nx/workspace 布局 | 确认命令仍是 `npx nx build web --configuration=oss`；oss 配置内置 `--deploy-url` 相对路径 |

## 升级后验收清单

用浏览器 harness（`go run -tags gtk3 ./cmd/devserve` → http://127.0.0.1:8899/harness.html），
iframe 加载 `/p/<token>/dist/index.html`（token 每次安装都变，从 harness.html 源码里抓）：

- [ ] 登录页渲染，控制台零报错（iframe 的 `contentWindow.__webtoolErrors` 为空）
- [ ] 环境菜单三项：bitwarden.com / bitwarden.eu / 自托管…（**无 localhost**）
- [ ] 自托管对话框填 `https://<服务器>` 保存 → 指示器变"自托管 (主机名)"
- [ ] **等效重启验证**（wt8 核心）：`iframe.contentWindow.sessionStorage.clear()` 后
      重设 iframe.src → 仍恢复自托管；`localStorage` 里 `global_environment_environment`
      保存后即刻可见
- [ ] 切回 bitwarden.com 正常（不导航、原地切换）
- [ ] 登录后：设置 → 安全 → 会话超时 下拉有"从不"选项
- [ ] 登录后 SignalR 通知正常（devserve 日志有 `/notifications-hub` 的隧道连接）

任一项失败：先看 `~/.config/webtool/logs/plugin-errors.log`，再用 harness 复现。

## 验证与排障的坑（经验）

- **等效重启** ≠ iframe 重载：重载不关窗口，sessionStorage 还在。必须先
  `sessionStorage.clear()` 再重载，才能模拟 wt8 修复的进程重启场景
- **agent-browser** 的 close 会丢弃 transient profile（localStorage 也没了），
  不能用它模拟重启；验证会话要复用同一个 session 名
- 宿主桥 `bridge.js` 与插件目录由 devserve 从磁盘直读，改后无需重启；**真实应用**
  是 embed 的，需 `pnpm --dir frontend build` + `go build -tags gtk3 -o build/webtool .`
- 上游 vault 硬校验所有 API/SignalR URL 必须 `https://` 开头；TS 的 `Region` 类型
  不含 "LOCAL"，比较要写 `String(x) === "LOCAL"`
- 本机若网络不稳（自托管服务器直连超时），上游代理会跟随系统代理
  （env → GNOME gsettings / KDE kioslaverc → 直连），确认 clash 等在跑

## 为什么这些必须由我们补

官方 web vault 的三个部署假设在本地插件托管下全不成立：

1. **保险库与 API 同源部署**（环境由域名决定）→ 我们是 `wails://` 本地页面，
   环境必须显式持久化 + 用户可切
2. **公共电脑威胁模型**（sessionStorage、无"从不超时"）→ 单用户桌面场景，
   与官方浏览器插件/桌面端对齐
3. **切换区域=跳转新域名**（导航离开）→ 本地插件必须原地切换，出网走宿主代理

这些与 webtool 的代理/隧道/持久化底座是配套设计，升级时属于"必须重放"而非可选优化。
