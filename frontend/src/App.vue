<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { PluginInfo, Tab, Toast } from './types'
import { PluginService } from '../bindings/webtool/internal/hostservice'
import Sidebar from './components/Sidebar.vue'
import TabBar from './components/TabBar.vue'
import PluginView from './components/PluginView.vue'
import HomePage from './components/HomePage.vue'
import InstallModal from './components/InstallModal.vue'
import CaptureModal from './components/CaptureModal.vue'
import RulesModal from './components/RulesModal.vue'
import MarketplaceModal from './components/MarketplaceModal.vue'

const plugins = ref<PluginInfo[]>([])
const tabs = ref<Tab[]>([])
const activeKey = ref('home')
const showInstall = ref(false)
const showCapture = ref(false)
const showRules = ref(false)
const showMarket = ref(false)
const toasts = ref<Toast[]>([])
let toastSeq = 1

// 主题：默认浅色，偏好持久化到 localStorage
type Theme = 'light' | 'dark'
const theme = ref<Theme>((localStorage.getItem('webtool.theme') as Theme) || 'light')

function applyTheme(t: Theme) {
  document.documentElement.classList.toggle('dark', t === 'dark')
}
applyTheme(theme.value)

function toggleTheme() {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  localStorage.setItem('webtool.theme', theme.value)
  applyTheme(theme.value)
}

function toast(kind: Toast['kind'], text: string) {
  const id = toastSeq++
  toasts.value.push({ id, kind, text })
  setTimeout(() => {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }, 3200)
}

async function refresh() {
  try {
    plugins.value = await PluginService.ListPlugins()
    await refreshIcons(plugins.value)
  } catch (e) {
    toast('err', `插件列表加载失败: ${e}`)
  }
}

// 图标统一解析：emoji 直显；http(s) 网络加载；其余视为插件目录内相对路径，
// 经 token 资源服务取回（zip 自带 favicon/logo 或安装时本地化的远程图标）
interface ResolvedIcon {
  emoji?: string
  url?: string
  letter?: string
}

const iconMap = ref<Record<string, ResolvedIcon>>({})

async function resolveIcon(p: PluginInfo): Promise<ResolvedIcon> {
  const icon = (p.icon || '').trim()
  if (!icon) {
    return { letter: p.name.slice(0, 1).toUpperCase() }
  }
  if (/^https?:\/\//.test(icon)) return { url: icon }
  if (/^[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u.test(icon) && !icon.includes('/')) {
    return { emoji: icon.slice(0, 2) }
  }
  try {
    const url = await PluginService.PluginAssetURL(p.id, icon.replace(/^\.\//, ''))
    return { url }
  } catch {
    /* 回落到首字母 */
  }
  return { letter: p.name.slice(0, 1).toUpperCase() }
}

async function refreshIcons(list: PluginInfo[]) {
  const entries = await Promise.all(
    list.map(async (p) => [p.id, await resolveIcon(p)] as const),
  )
  iconMap.value = Object.fromEntries(entries)
}

async function openPlugin(p: PluginInfo) {
  if (!p.enabled) {
    toast('err', `插件「${p.name}」已停用`)
    return
  }
  try {
    const src = await PluginService.OpenPlugin(p.id)
    if (!src) return
    const exist = tabs.value.find((t) => t.key === p.id)
    const icon = iconMap.value[p.id] ?? (await resolveIcon(p))
    if (exist) {
      // 已存在（含隐藏）：直接切回，不重新加载 src（保留 iframe 内存状态）
      exist.hidden = false
    } else {
      tabs.value.push({
        key: p.id,
        title: p.name,
        src,
        iconEmoji: icon.emoji ?? icon.letter,
        iconUrl: icon.url,
        sandbox: p.sandbox ?? false,
      })
    }
    activeKey.value = p.id
  } catch (e) {
    toast('err', `打开插件失败: ${e}`)
  }
}

function closeTab(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  // 标记隐藏而非销毁：iframe 保持存活，插件内存状态（如 Bitwarden 解锁态）不丢失。
  // 再次从侧边栏打开时直接切回，无需重新加载。
  tab.hidden = true
  if (activeKey.value === key) {
    const visible = tabs.value.filter((t) => !t.hidden)
    const idx = tabs.value.indexOf(tab)
    activeKey.value =
      visible[idx - 1]?.key ?? visible[idx]?.key ?? visible[0]?.key ?? 'home'
  }
}

// destroyTab：真正从 DOM 移除（卸载插件 / 停用时调用）
function destroyTab(key: string) {
  const idx = tabs.value.findIndex((t) => t.key === key)
  if (idx < 0) return
  tabs.value.splice(idx, 1)
  if (activeKey.value === key) {
    const visible = tabs.value.filter((t) => !t.hidden)
    activeKey.value = visible[idx - 1]?.key ?? visible[0]?.key ?? 'home'
  }
}

async function toggleEnabled(p: PluginInfo) {
  try {
    await PluginService.SetPluginEnabled(p.id, !p.enabled)
    if (p.enabled) destroyTab(p.id) // 停用时真正销毁标签
    await refresh()
  } catch (e) {
    toast('err', `${p.enabled ? '停用' : '启用'}失败: ${e}`)
  }
}

async function uninstall(p: PluginInfo) {
  if (!window.confirm(`确定卸载插件「${p.name}」？其本地数据也会被删除。`)) return
  try {
    await PluginService.UninstallPlugin(p.id, true)
    destroyTab(p.id)
    toast('ok', `已卸载 ${p.name}`)
    await refresh()
  } catch (e) {
    toast('err', `卸载失败: ${e}`)
  }
}

async function installFromFile() {
  try {
    const info = await PluginService.InstallFromFile()
    if (info) {
      toast('ok', `已安装 ${info.name}`)
      await refresh()
      await openPlugin(info)
    }
  } catch (e) {
    toast('err', `安装失败: ${e}`)
  }
}

async function installFromURL(url: string) {
  try {
    const info = await PluginService.InstallFromURL(url)
    if (info) {
      toast('ok', `已安装 ${info.name}`)
      showInstall.value = false
      await refresh()
      await openPlugin(info)
    }
  } catch (e) {
    toast('err', `安装失败: ${e}`)
  }
}

// ---- 网页捕获（P2）----

async function captureWeb(name: string, url: string) {
  try {
    const info = await PluginService.CreateWebPlugin(name, url)
    if (info) {
      toast('ok', `已捕获 ${info.name}（录制模式）`)
      showCapture.value = false
      await refresh()
      await openPlugin(info)
    }
  } catch (e) {
    toast('err', `捕获失败: ${e}`)
  }
}

// 切换 web 插件模式：固化(offline) / 回到录制(record)
async function pinPlugin(p: PluginInfo) {
  const next = p.mode === 'offline' ? 'record' : 'offline'
  try {
    await PluginService.SetPluginMode(p.id, next)
    toast('ok', next === 'offline' ? `已固化 ${p.name}（离线可用）` : `${p.name} 已回到录制模式`)
    await refresh()
    // 重开标签让新模式立即呈现（需真正重载，故用 destroyTab）
    destroyTab(p.id)
    const updated = plugins.value.find((x) => x.id === p.id)
    if (updated) await openPlugin(updated)
  } catch (e) {
    toast('err', `切换模式失败: ${e}`)
  }
}

// 市场安装完成：关闭弹窗、刷新并打开
async function onMarketInstalled(info: PluginInfo) {
  showMarket.value = false
  toast('ok', `已安装 ${info.name}`)
  await refresh()
  await openPlugin(info)
}

const activeTab = computed(() => tabs.value.find((t) => t.key === activeKey.value))
// 标签栏只显示未隐藏的标签
const visibleTabs = computed(() => tabs.value.filter((t) => !t.hidden))

onMounted(refresh)
</script>

<template>
  <div
    class="flex h-screen w-screen overflow-hidden bg-[#f6f7f9] text-zinc-800 dark:bg-[#101216] dark:text-zinc-100"
  >
    <Sidebar
      :plugins="plugins"
      :active-key="activeKey"
      :theme="theme"
      :icon-map="iconMap"
      @open="openPlugin"
      @toggle="toggleEnabled"
      @uninstall="uninstall"
      @install="showInstall = true"
      @capture="showCapture = true"
      @rules="showRules = true"
      @market="showMarket = true"
      @pin="pinPlugin"
      @toggle-theme="toggleTheme"
    />

    <div class="flex min-w-0 flex-1 flex-col">
      <TabBar
        :tabs="visibleTabs"
        :active-key="activeKey"
        @select="(k: string) => (activeKey = k)"
        @close="closeTab"
        @home="activeKey = 'home'"
      />

      <div class="relative min-h-0 flex-1">
        <HomePage
          v-show="activeKey === 'home'"
          :plugins="plugins"
          :count="plugins.length"
          :icon-map="iconMap"
          @open="openPlugin"
          @install="showInstall = true"
        />
        <PluginView
          v-for="t in tabs"
          :key="t.key"
          v-show="t.key === activeKey"
          :tab="t"
        />
      </div>
    </div>

    <InstallModal
      v-if="showInstall"
      @close="showInstall = false"
      @pick-file="installFromFile"
      @submit-url="installFromURL"
    />

    <CaptureModal
      v-if="showCapture"
      @close="showCapture = false"
      @submit="captureWeb"
    />

    <RulesModal v-if="showRules" @close="showRules = false" />

    <MarketplaceModal
      v-if="showMarket"
      :installed="plugins"
      @close="showMarket = false"
      @installed="onMarketInstalled"
    />

    <!-- 轻量 toast -->
    <div class="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
      <div
        v-for="t in toasts"
        :key="t.id"
        class="rounded-lg border px-4 py-2 text-sm shadow-lg backdrop-blur"
        :class="
          t.kind === 'ok'
            ? 'border-emerald-700/60 bg-emerald-900/80 text-emerald-100'
            : 'border-red-700/60 bg-red-900/80 text-red-100'
        "
      >
        {{ t.text }}
      </div>
    </div>
  </div>
</template>
