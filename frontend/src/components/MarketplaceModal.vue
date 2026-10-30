<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { PluginInfo } from '../types'
import { PluginService } from '../../bindings/webtool/internal/hostservice'

interface MarketEntry {
  id: string
  name: string
  version: string
  description: string
  author?: string
  icon?: string
  category?: string
  homepage?: string
  permissions?: string[]
  keywords?: string[]
  downloads?: number
  zipUrl: string
}

const props = defineProps<{ installed: PluginInfo[] }>()

const emit = defineEmits<{
  close: []
  installed: [info: PluginInfo]
}>()

const entries = ref<MarketEntry[]>([])
const loading = ref(true)
const error = ref('')
const query = ref('')
const installing = ref<Record<string, boolean>>({})

const installedIds = computed(() => new Set(props.installed.map((p) => p.id)))

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return entries.value
  return entries.value.filter(
    (e) =>
      e.name.toLowerCase().includes(q) ||
      e.id.toLowerCase().includes(q) ||
      (e.description || '').toLowerCase().includes(q) ||
      (e.keywords || []).some((k) => k.toLowerCase().includes(q)),
  )
})

function iconOf(e: MarketEntry): { emoji?: string; url?: string; letter?: string } {
  const icon = (e.icon || '').trim()
  if (/^https?:\/\//.test(icon)) return { url: icon }
  if (icon && !icon.includes('/')) return { emoji: icon.slice(0, 2) }
  return { letter: e.name.slice(0, 1).toUpperCase() }
}

onMounted(async () => {
  try {
    entries.value = (await PluginService.ListMarketplace()) ?? []
  } catch (e) {
    error.value = `市场加载失败（需要联网）: ${e}`
  } finally {
    loading.value = false
  }
})

async function install(e: MarketEntry) {
  installing.value = { ...installing.value, [e.id]: true }
  try {
    const info = await PluginService.InstallMarketplacePlugin(e.zipUrl, e.id)
    if (info) emit('installed', info)
  } catch (err) {
    error.value = `安装 ${e.name} 失败: ${err}`
  } finally {
    installing.value = { ...installing.value, [e.id]: false }
  }
}
</script>

<template>
  <div
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 backdrop-blur-sm"
    @click.self="emit('close')"
  >
    <div
      class="flex h-[600px] w-[720px] flex-col rounded-xl border border-zinc-200 bg-white shadow-2xl dark:border-zinc-700 dark:bg-[#1a1d23]"
    >
      <div class="flex items-center gap-3 px-5 pb-3 pt-4">
        <div class="text-sm font-semibold text-zinc-800 dark:text-zinc-100">插件市场</div>
        <div class="text-xs text-zinc-400 dark:text-zinc-500">unihub 官方源</div>
        <input
          v-model="query"
          type="search"
          placeholder="搜索名称 / 关键词…"
          class="ml-auto w-52 rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-[13px] text-zinc-800 outline-none placeholder:text-zinc-400 focus:border-sky-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200"
        />
        <button
          class="text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto px-5 pb-2">
        <div v-if="loading" class="flex h-full items-center justify-center text-sm text-zinc-400">
          正在拉取市场目录…
        </div>
        <div v-else-if="error && entries.length === 0" class="mt-16 text-center text-sm text-red-500">
          {{ error }}
        </div>
        <template v-else>
          <div
            v-for="e in filtered"
            :key="e.id"
            class="mb-2 flex items-center gap-3 rounded-lg border border-zinc-200 bg-zinc-50/60 px-4 py-3 dark:border-zinc-800 dark:bg-zinc-900/40"
          >
            <img
              v-if="iconOf(e).url"
              :src="iconOf(e).url"
              class="h-8 w-8 shrink-0 rounded"
              referrerpolicy="no-referrer"
            />
            <span v-else-if="iconOf(e).emoji" class="w-8 shrink-0 text-center text-xl">{{ iconOf(e).emoji }}</span>
            <span
              v-else
              class="flex h-8 w-8 shrink-0 items-center justify-center rounded bg-sky-100 text-sm font-semibold text-sky-700 dark:bg-sky-900/40 dark:text-sky-300"
            >{{ iconOf(e).letter }}</span>

            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-2">
                <span class="truncate text-[13px] font-medium text-zinc-800 dark:text-zinc-200">{{ e.name }}</span>
                <span class="shrink-0 text-[11px] text-zinc-400">v{{ e.version }}</span>
                <span
                  v-if="e.permissions?.length"
                  class="shrink-0 rounded bg-amber-100 px-1.5 text-[10px] text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
                  :title="`权限: ${e.permissions.join(', ')}`"
                >
                  {{ e.permissions.length }} 项权限
                </span>
              </div>
              <div class="truncate text-xs text-zinc-500 dark:text-zinc-400">
                {{ e.description || e.id }}<span v-if="e.author"> · {{ e.author }}</span>
              </div>
            </div>

            <button
              v-if="installedIds.has(e.id)"
              disabled
              class="shrink-0 rounded-lg bg-zinc-100 px-3 py-1.5 text-xs text-zinc-400 dark:bg-zinc-800 dark:text-zinc-500"
            >
              已安装
            </button>
            <button
              v-else
              class="shrink-0 rounded-lg bg-sky-600 px-3 py-1.5 text-xs text-white transition-colors hover:bg-sky-500 disabled:opacity-50"
              :disabled="installing[e.id]"
              @click="install(e)"
            >
              {{ installing[e.id] ? '安装中…' : '安装' }}
            </button>
          </div>
          <div v-if="!loading && filtered.length === 0" class="mt-16 text-center text-sm text-zinc-400">
            没有匹配的插件
          </div>
        </template>
      </div>

      <div v-if="error && entries.length > 0" class="px-5 pb-3 text-xs text-red-500">
        {{ error }}
      </div>
    </div>
  </div>
</template>
