<script setup lang="ts">
import { computed } from 'vue'
import type { PluginInfo } from '../types'

interface ResolvedIcon {
  emoji?: string
  url?: string
  letter?: string
}

const props = defineProps<{
  plugins: PluginInfo[]
  activeKey: string
  theme: 'light' | 'dark'
  iconMap: Record<string, ResolvedIcon>
}>()

const emit = defineEmits<{
  open: [p: PluginInfo]
  toggle: [p: PluginInfo]
  uninstall: [p: PluginInfo]
  install: []
  capture: []
  rules: []
  market: []
  pin: [p: PluginInfo]
  'toggle-theme': []
}>()

// 按分类分组，保持稳定顺序
const groups = computed(() => {
  const map = new Map<string, PluginInfo[]>()
  for (const p of props.plugins) {
    const cat = p.category || 'tool'
    if (!map.has(cat)) map.set(cat, [])
    map.get(cat)!.push(p)
  }
  return Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]))
})

function iconOf(p: PluginInfo): ResolvedIcon {
  return (
    props.iconMap[p.id] ?? { letter: p.name.slice(0, 1).toUpperCase() }
  )
}
</script>

<template>
  <aside
    class="flex h-full w-60 shrink-0 flex-col border-r border-zinc-200 bg-white dark:border-zinc-800 dark:bg-[#16181d]"
  >
    <div class="flex items-center gap-2 px-4 py-3.5">
      <span class="text-lg">🧰</span>
      <span class="text-sm font-semibold tracking-wide text-zinc-800 dark:text-zinc-100">WebTool</span>
      <button
        class="ml-auto rounded-md px-1.5 py-0.5 text-sm text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
        :title="theme === 'dark' ? '切换到浅色' : '切换到深色'"
        @click="emit('toggle-theme')"
      >
        {{ theme === 'dark' ? '☀️' : '🌙' }}
      </button>
    </div>

    <nav class="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
      <div
        v-if="groups.length === 0"
        class="mt-8 px-3 text-center text-xs leading-6 text-zinc-400 dark:text-zinc-500"
      >
        还没有插件
        <br />
        点击下方「安装插件」导入 zip
      </div>

      <div v-for="[cat, items] in groups" :key="cat" class="mb-3">
        <div
          class="px-3 pb-1 pt-2 text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:text-zinc-500"
        >
          {{ cat }}
        </div>
        <div
          v-for="p in items"
          :key="p.id"
          class="group flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-1.5 transition-colors"
          :class="
            activeKey === p.id
              ? 'bg-zinc-200/80 text-zinc-900 dark:bg-zinc-700/60 dark:text-white'
              : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800/70'
          "
          :title="`${p.name} v${p.version}${p.description ? ' — ' + p.description : ''}`"
          @click="emit('open', p)"
        >
          <img
            v-if="iconOf(p).url"
            :src="iconOf(p).url"
            class="h-4.5 w-4.5 shrink-0 rounded"
            referrerpolicy="no-referrer"
          />
          <span v-else-if="iconOf(p).emoji" class="w-4.5 shrink-0 text-center text-sm leading-none">{{ iconOf(p).emoji }}</span>
          <span
            v-else
            class="flex h-4.5 w-4.5 shrink-0 items-center justify-center rounded bg-zinc-200 text-[10px] font-semibold text-zinc-600 dark:bg-zinc-700 dark:text-zinc-300"
          >{{ iconOf(p).letter }}</span>
          <span class="min-w-0 flex-1 truncate text-[13px]">{{ p.name }}</span>

          <!-- 悬停操作 -->
          <span class="hidden shrink-0 items-center gap-1 group-hover:flex" @click.stop>
            <button
              v-if="p.type === 'web'"
              class="rounded px-1 text-[11px] text-zinc-400 hover:text-sky-600 dark:hover:text-sky-400"
              :title="p.mode === 'offline' ? '回到在线模式' : '固化为离线插件'"
              @click="emit('pin', p)"
            >
              {{ p.mode === 'offline' ? '🔄' : '📌' }}
            </button>
            <button
              class="rounded px-1 text-[11px] text-zinc-400 hover:text-amber-600 dark:hover:text-amber-300"
              :title="p.enabled ? '停用' : '启用'"
              @click="emit('toggle', p)"
            >
              {{ p.enabled ? '⏸' : '▶' }}
            </button>
            <button
              class="rounded px-1 text-[11px] text-zinc-400 hover:text-red-500 dark:hover:text-red-400"
              title="卸载"
              @click="emit('uninstall', p)"
            >
              ✕
            </button>
          </span>
          <span
            v-if="!p.enabled"
            class="shrink-0 text-[10px] text-zinc-400 group-hover:hidden dark:text-zinc-500"
          >
            停
          </span>
          <span
            v-else-if="p.type === 'web' && p.mode"
            class="shrink-0 text-[10px] text-zinc-400 group-hover:hidden dark:text-zinc-500"
            :title="p.mode === 'offline' ? '离线' : p.mode === 'record' ? '录制中' : '在线'"
          >
            {{ p.mode === 'offline' ? '离' : p.mode === 'record' ? '录' : '' }}
          </span>
        </div>
      </div>
    </nav>

    <div class="space-y-1.5 border-t border-zinc-200 p-2 dark:border-zinc-800">
      <button
        class="flex w-full items-center justify-center gap-1.5 rounded-lg bg-zinc-100 px-3 py-2 text-[13px] text-zinc-700 transition-colors hover:bg-zinc-200 dark:bg-zinc-700/50 dark:text-zinc-200 dark:hover:bg-zinc-600/60"
        @click="emit('market')"
      >
        <span class="text-base leading-none">🧩</span> 插件市场
      </button>
      <button
        class="flex w-full items-center justify-center gap-1.5 rounded-lg bg-zinc-100 px-3 py-2 text-[13px] text-zinc-700 transition-colors hover:bg-zinc-200 dark:bg-zinc-700/50 dark:text-zinc-200 dark:hover:bg-zinc-600/60"
        @click="emit('install')"
      >
        <span class="text-base leading-none">＋</span> 安装插件
      </button>
      <div class="flex gap-1.5">
        <button
          class="flex flex-1 items-center justify-center gap-1 rounded-lg px-2 py-1.5 text-xs text-zinc-500 transition-colors hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
          title="把在线网页固化为离线插件"
          @click="emit('capture')"
        >
          📷 捕获网页
        </button>
        <button
          class="flex flex-1 items-center justify-center gap-1 rounded-lg px-2 py-1.5 text-xs text-zinc-500 transition-colors hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
          title="编辑全局拦截规则"
          @click="emit('rules')"
        >
          🛡️ 拦截规则
        </button>
      </div>
    </div>
  </aside>
</template>
