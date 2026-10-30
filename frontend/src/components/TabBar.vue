<script setup lang="ts">
import type { Tab } from '../types'

defineProps<{
  tabs: Tab[]
  activeKey: string
}>()

const emit = defineEmits<{
  select: [key: string]
  close: [key: string]
  home: []
}>()
</script>

<template>
  <div
    class="flex h-10 shrink-0 items-stretch border-b border-zinc-200 bg-white dark:border-zinc-800 dark:bg-[#13151a]"
  >
    <!-- 首页标签 -->
    <button
      class="flex w-28 shrink-0 items-center justify-center gap-1.5 border-r border-zinc-200 text-[13px] transition-colors dark:border-zinc-800"
      :class="
        activeKey === 'home'
          ? 'bg-zinc-100 text-zinc-900 dark:bg-zinc-700/40 dark:text-white'
          : 'text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200'
      "
      @click="emit('home')"
    >
      <span>⌂</span> 主页
    </button>

    <div class="flex min-w-0 flex-1 items-stretch overflow-x-auto">
      <div
        v-for="t in tabs"
        :key="t.key"
        class="group flex min-w-0 max-w-52 shrink-0 cursor-pointer items-center gap-2 border-r border-zinc-200 px-3 text-[13px] transition-colors dark:border-zinc-800"
        :class="
          activeKey === t.key
            ? 'bg-zinc-100 text-zinc-900 dark:bg-zinc-700/40 dark:text-white'
            : 'text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200'
        "
        @click="emit('select', t.key)"
      >
        <img
          v-if="t.iconUrl"
          :src="t.iconUrl"
          class="h-4 w-4 shrink-0 rounded"
          referrerpolicy="no-referrer"
        />
        <span v-else-if="t.iconEmoji" class="shrink-0 text-sm leading-none">{{ t.iconEmoji }}</span>
        <span class="min-w-0 flex-1 truncate">{{ t.title }}</span>
        <button
          class="shrink-0 rounded px-1 text-xs text-zinc-400 opacity-0 transition-opacity hover:text-red-500 group-hover:opacity-100 dark:text-zinc-500 dark:hover:text-red-400"
          @click.stop="emit('close', t.key)"
        >
          ✕
        </button>
      </div>
    </div>
  </div>
</template>
