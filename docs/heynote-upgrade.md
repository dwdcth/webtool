# Heynote 插件升级指南

webtool 内置的 Heynote 插件基于 [heyman/heynote](https://github.com/heyman/heynote)
官方 **webapp 构建目标** + 我们的适配补丁打包而成。上游发新版后按本文档重新打包即可。

## 我们改了什么（升级时需要重放的补丁）

全部改动以 git diff 形式留档在 **`patches/heynote-webapp.patch`**，只动两个地方：

1. **`webapp/bridge.js`**（主要改动）：
   - 通用弹出菜单组件 `wtShowMenu` + 六个菜单（编辑器/标签页/缓冲区树×3/拼写/主菜单）
   - 图片存储：`buffer.saveImage` 优先走宿主 SQLite（`window.unihub.storage.setBlob`），
     localStorage 兜底；启动预载 `wtPreloadImages`；`window.__wtResolveImage`
   - 文本库抽象层 `libGet/libSet/libDel/libKeys` + `metaGet/metaSet`：
     笔记(wtbuf:)/目录(wtmeta:dirs)/标签状态(wtmeta:tabs) 全走宿主 SQLite，
     含 localStorage 一次性迁移
   - `buffer.selectLocation` no-op
2. **`src/editor/image/widget.js`**：`img.src` 两处支持 `wtimg-*` 文件名解析为 dataURL
   （标签里的 file 参数不能含 dataURL——分号会破坏 `<∞img;...∞>` 参数分隔）

## 升级步骤

```bash
# 1. 拉取上游新版本（记下版本号，写入步骤 4 的清单）
cd /tmp && rm -rf heynote && git clone --depth=1 https://github.com/heyman/heynote
cd heynote

# 2. 打补丁（冲突时按下节"冲突处理"手动合并）
git apply --reject /path/to/webtool/patches/heynote-webapp.patch
# 有 .rej 文件时逐个手动合入，改完删除 .rej/.orig

# 3. 构建（相对 base 适配插件 /p/<token>/ 命名空间）
npm install
npx vite build webapp --base=./

# 4. 打包插件 zip（manifest 里 version 改成上游版本号）
mkdir -p /tmp/heynote-plugin && cd /tmp/heynote-plugin
rm -rf dist && cp -r /tmp/heynote/webapp/dist dist
#    编辑 package.json：unihub.entry=dist/index.html、icon=dist/icon.png、
#    permissions=["storage","clipboard"]、version=<上游版本>
python3 -c "
import zipfile, os
with zipfile.ZipFile('<webtool>/plugin/heynote.zip', 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for root, dirs, files in os.walk('.'):
        for f in files:
            p = os.path.join(root, f)
            z.write(p, os.path.relpath(p, '.'))
"

# 5. 安装到 webtool（应用内卸载重装，或命令行）
cd <webtool>
python3 -c "
import json, shutil
p = '$HOME/.config/webtool/plugins.json'
d = json.load(open(p)); d.pop('heynote', None)
json.dump(d, open(p, 'w'), indent=2)
shutil.rmtree('$HOME/.config/webtool/plugins/heynote', ignore_errors=True)
"
go run -tags gtk3 ./cmd/devsetup plugin/heynote.zip

# 6. 更新补丁档案（把你的最终改动重新 diff 留档）
cd /tmp/heynote && git diff webapp/bridge.js src/ > <webtool>/patches/heynote-webapp.patch
```

> 数据不受升级影响：笔记/图片都在 `plugin-data/heynote.blob.db`，与插件目录分离。

## 冲突处理（按概率排序）

| 冲突位置 | 原因 | 处理 |
|---|---|---|
| `webapp/bridge.js` 的 buffer 方法 | 上游改了 localStorage 存储逻辑 | 保持我们的 `lib*` 抽象层签名不变，把上游新逻辑映射到 libSet/libGet |
| `bridge.js` 的 `mainProcess.invoke` switch | 上游加了新 IPC 调用 | 新 case 先留空跑通，再决定是否需要自绘 UI（参考六个菜单的写法） |
| `widget.js` 的 `img.src` | 上游重构图片组件 | 核心不变：渲染时把 `wtimg-*` 解析成 dataURL（`__wtResolveImage`） |
| vite 构建配置 | 上游改了 webapp 目录结构 | 确认构建命令仍是 `vite build webapp`，`--base=./` 必须保留 |

## 升级后验收清单

用浏览器 harness（`go run -tags gtk3 ./cmd/devserve` → http://127.0.0.1:8899/harness.html）或直接在应用里逐项过：

- [ ] 编辑器渲染，Ctrl+Enter 建块，块头可切换语言
- [ ] JSON/math 块有语法高亮和计算结果
- [ ] 中文输入法正常输入，2 秒防抖保存
- [ ] 截图 Ctrl+V：图片真实显示（非占位符），重启应用仍在
- [ ] 编辑器/标签页/缓冲区树右键菜单、左上角主菜单都弹出
- [ ] 新建/重命名/删除缓冲区正常，库列表正确
- [ ] `~/.config/webtool/plugin-data/heynote.blob.db` 中出现新写入的 `wtbuf:` 键

任一项失败：先看 `~/.config/webtool/logs/plugin-errors.log`（宿主的插件错误采集），
再用 harness 复现（控制台零报错为底线）。

## 为什么有些功能必须由我们补

桌面版 heynote 的这些能力走 Electron 原生 API，官方 webapp 桥没实现
（他们没部署过网页版）：全部右键/主菜单（原生 Menu）、`saveImage`（写磁盘）、
`selectLocation`（目录选择）。而 `navigator.clipboard.read()` 在 WebKitGTK
无授权机制，需要宿主桥兜底——这些与 webtool 的能力 API 设计是配套的，
升级时属于"必须重放"而非"可选优化"。
