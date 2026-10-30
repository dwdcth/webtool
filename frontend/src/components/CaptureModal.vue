<script setup lang="ts">
import { ref } from 'vue'

const emit = defineEmits<{
  close: []
  submit: [name: string, url: string]
}>()

const name = ref('')
const url = ref('')
const busy = ref(false)

function submit() {
  const u = url.value.trim()
  if (!/^https?:\/\//.test(u)) return
  busy.value = true
  emit('submit', name.value.trim(), u)
}
</script>

<template>
  <div
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 backdrop-blur-sm"
    @click.self="emit('close')"
  >
    <div
      class="w-[460px] rounded-xl border border-zinc-200 bg-white p-5 shadow-2xl dark:border-zinc-700 dark:bg-[#1a1d23]"
    >
      <div class="mb-1 flex items-center justify-between">
        <div class="text-sm font-semibold text-zinc-800 dark:text-zinc-100">捕获网页为离线插件</div>
        <button
          class="text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>
      <p class="mb-4 text-xs leading-5 text-zinc-500 dark:text-zinc-400">
        输入在线工具网址，默认进入<b>录制模式</b>——浏览过程中所有资源自动缓存到本地；
        之后在插件上点 📌 固化，即可完全离线使用。
      </p>

      <div class="text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:text-zinc-500">
        网页地址
      </div>
      <input
        v-model="url"
        type="url"
        placeholder="https://example.com/tool"
        class="mt-1.5 w-full rounded-lg border border-zinc-300 bg-white px-3 py-2 text-[13px] text-zinc-800 outline-none placeholder:text-zinc-400 focus:border-sky-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:placeholder:text-zinc-600"
        @keydown.enter="submit"
      />

      <div class="mt-3 text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:text-zinc-500">
        名称（可选）
      </div>
      <input
        v-model="name"
        type="text"
        placeholder="自动取自网址"
        class="mt-1.5 w-full rounded-lg border border-zinc-300 bg-white px-3 py-2 text-[13px] text-zinc-800 outline-none placeholder:text-zinc-400 focus:border-sky-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:placeholder:text-zinc-600"
        @keydown.enter="submit"
      />

      <div class="mt-5 flex justify-end gap-2">
        <button
          class="rounded-lg px-4 py-2 text-[13px] text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          class="rounded-lg bg-sky-600 px-4 py-2 text-[13px] text-white transition-colors hover:bg-sky-500 disabled:opacity-50"
          :disabled="!/^https?:\/\//.test(url.trim()) || busy"
          @click="submit"
        >
          打开并录制
        </button>
      </div>
    </div>
  </div>
</template>
