<script setup lang="ts">
import type { PluginInfo } from '../types'

interface ResolvedIcon {
  emoji?: string
  url?: string
  letter?: string
}

const props = defineProps<{
  plugins: PluginInfo[]
  count: number
  iconMap: Record<string, ResolvedIcon>
}>()

const emit = defineEmits<{
  open: [p: PluginInfo]
  install: []
}>()

function iconOf(p: PluginInfo): ResolvedIcon {
  return props.iconMap[p.id] ?? { letter: p.name.slice(0, 1).toUpperCase() }
}
</script>

<template>
  <div class="absolute inset-0 overflow-y-auto">
    <div class="mx-auto max-w-3xl px-8 py-12">
      <div class="mb-2 text-2xl font-semibold text-zinc-800 dark:text-zinc-100">网页工具箱</div>
      <p class="mb-8 text-sm leading-6 text-zinc-500 dark:text-zinc-400">
        安装 zip 插件或把在线工具固化为离线插件；拦截规则数据驱动，不再写死。
      </p>

      <div
        v-if="count === 0"
        class="rounded-xl border border-dashed border-zinc-300 bg-white p-10 text-center dark:border-zinc-700 dark:bg-zinc-900/40"
      >
        <div class="mb-3 text-4xl">📦</div>
        <div class="mb-1 text-sm text-zinc-700 dark:text-zinc-300">还没有安装任何插件</div>
        <p class="mb-5 text-xs leading-5 text-zinc-400 dark:text-zinc-500">
          支持 unihub 格式的插件包（zip 根目录含 package.json），
          <br />
          也兼容无清单的旧版 zip（如 ctool.zip）。
        </p>
        <button
          class="rounded-lg bg-sky-600 px-4 py-2 text-sm text-white transition-colors hover:bg-sky-500"
          @click="emit('install')"
        >
          安装第一个插件
        </button>
      </div>

      <div v-else class="grid grid-cols-2 gap-3 md:grid-cols-3">
        <button
          v-for="p in plugins"
          :key="p.id"
          class="flex items-start gap-3 rounded-xl border border-zinc-200 bg-white p-4 text-left transition-colors hover:border-zinc-300 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900/40 dark:hover:border-zinc-600 dark:hover:bg-zinc-800/50"
          :class="!p.enabled && 'opacity-50'"
          @click="p.enabled && emit('open', p)"
        >
          <img
            v-if="iconOf(p).url"
            :src="iconOf(p).url"
            class="mt-0.5 h-6 w-6 shrink-0 rounded"
            referrerpolicy="no-referrer"
          />
          <span v-else-if="iconOf(p).emoji" class="mt-0.5 text-xl leading-none">{{ iconOf(p).emoji }}</span>
          <span
            v-else
            class="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded bg-zinc-200 text-xs font-semibold text-zinc-600 dark:bg-zinc-700 dark:text-zinc-300"
          >{{ iconOf(p).letter }}</span>
          <span class="min-w-0">
            <span class="block truncate text-sm text-zinc-800 dark:text-zinc-200">{{ p.name }}</span>
            <span class="mt-0.5 block truncate text-xs text-zinc-400 dark:text-zinc-500">
              {{ p.description || `v${p.version} · ${p.category}` }}
            </span>
          </span>
        </button>
      </div>
    </div>
  </div>
</template>
