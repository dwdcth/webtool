<script setup lang="ts">
import { ref } from 'vue'

const emit = defineEmits<{
  close: []
  'pick-file': []
  'submit-url': [url: string]
}>()

const url = ref('')
const installing = ref(false)

async function submitUrl() {
  const u = url.value.trim()
  if (!u) return
  installing.value = true
  try {
    emit('submit-url', u)
  } finally {
    installing.value = false
  }
}
</script>

<template>
  <div
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 backdrop-blur-sm"
    @click.self="emit('close')"
  >
    <div
      class="w-[420px] rounded-xl border border-zinc-200 bg-white p-5 shadow-2xl dark:border-zinc-700 dark:bg-[#1a1d23]"
    >
      <div class="mb-4 flex items-center justify-between">
        <div class="text-sm font-semibold text-zinc-800 dark:text-zinc-100">安装插件</div>
        <button
          class="text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>

      <!-- 本地 zip -->
      <button
        class="mb-5 flex w-full flex-col items-center gap-2 rounded-lg border border-dashed border-zinc-300 bg-zinc-50 px-4 py-6 transition-colors hover:border-sky-500 hover:bg-sky-50/50 dark:border-zinc-600 dark:bg-zinc-800/40 dark:hover:border-sky-500 dark:hover:bg-zinc-800"
        @click="emit('pick-file')"
      >
        <span class="text-2xl">🗂️</span>
        <span class="text-[13px] text-zinc-700 dark:text-zinc-300">选择本地 .zip 插件包</span>
        <span class="text-[11px] text-zinc-400 dark:text-zinc-500">
          unihub 格式（package.json）或旧版无清单 zip
        </span>
      </button>

      <!-- URL 安装 -->
      <div class="text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:text-zinc-500">
        从 URL 安装
      </div>
      <div class="mt-2 flex gap-2">
        <input
          v-model="url"
          type="url"
          placeholder="https://…/plugin.zip"
          class="min-w-0 flex-1 rounded-lg border border-zinc-300 bg-white px-3 py-2 text-[13px] text-zinc-800 outline-none placeholder:text-zinc-400 focus:border-sky-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:placeholder:text-zinc-600"
          @keydown.enter="submitUrl"
        />
        <button
          class="shrink-0 rounded-lg bg-sky-600 px-4 text-[13px] text-white transition-colors hover:bg-sky-500 disabled:opacity-50"
          :disabled="!url.trim() || installing"
          @click="submitUrl"
        >
          下载
        </button>
      </div>

      <p class="mt-4 text-[11px] leading-5 text-zinc-400 dark:text-zinc-500">
        安装时会展示插件声明的权限（clipboard / storage / fs / http / spawn…），
        能力调用在 Go 侧按插件身份强制校验。
      </p>
    </div>
  </div>
</template>
