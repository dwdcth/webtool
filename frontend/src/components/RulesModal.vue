<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { PluginService } from '../../bindings/webtool/internal/hostservice'

const emit = defineEmits<{ close: [] }>()

const text = ref('')
const error = ref('')
const saved = ref(false)
const loading = ref(true)

onMounted(async () => {
  try {
    const rules = await PluginService.GetGlobalRules()
    text.value = JSON.stringify(rules ?? [], null, 2)
  } catch (e) {
    error.value = `读取失败: ${e}`
  } finally {
    loading.value = false
  }
})

async function save() {
  error.value = ''
  saved.value = false
  let rules: unknown
  try {
    rules = JSON.parse(text.value)
  } catch (e) {
    error.value = `JSON 解析失败: ${e}`
    return
  }
  if (!Array.isArray(rules)) {
    error.value = '顶层必须是规则数组 []'
    return
  }
  try {
    await PluginService.SaveGlobalRules(rules as never)
    saved.value = true
    setTimeout(() => (saved.value = false), 2000)
  } catch (e) {
    error.value = `保存失败: ${e}`
  }
}
</script>

<template>
  <div
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 backdrop-blur-sm"
    @click.self="emit('close')"
  >
    <div
      class="flex h-[560px] w-[640px] flex-col rounded-xl border border-zinc-200 bg-white p-5 shadow-2xl dark:border-zinc-700 dark:bg-[#1a1d23]"
    >
      <div class="mb-1 flex items-center justify-between">
        <div class="text-sm font-semibold text-zinc-800 dark:text-zinc-100">拦截规则（全局）</div>
        <button
          class="text-zinc-400 hover:text-zinc-600 dark:text-zinc-500 dark:hover:text-zinc-300"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>
      <p class="mb-3 text-xs leading-5 text-zinc-500 dark:text-zinc-400">
        作用于所有插件请求（含镜像代理回源）。按顺序匹配，命中即执行。旧版 ctool 的
        chrome_tool 拦截已迁移为这里的默认规则——不再写死，改完保存即热生效。
      </p>

      <textarea
        v-model="text"
        spellcheck="false"
        class="min-h-0 flex-1 resize-none rounded-lg border border-zinc-300 bg-zinc-50 p-3 font-mono text-xs leading-5 text-zinc-800 outline-none focus:border-sky-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200"
        :disabled="loading"
        placeholder="[]"
      />

      <div class="mt-2 flex items-center justify-between">
        <div class="text-xs">
          <span v-if="error" class="text-red-500">{{ error }}</span>
          <span v-else-if="saved" class="text-emerald-600 dark:text-emerald-400">✓ 已保存并热生效</span>
          <span v-else-if="loading" class="text-zinc-400">加载中…</span>
          <span v-else class="text-zinc-400 dark:text-zinc-500">
            action.type: pass | block | respond | rewrite | cache-first | no-cache
          </span>
        </div>
        <button
          class="rounded-lg bg-sky-600 px-4 py-2 text-[13px] text-white transition-colors hover:bg-sky-500"
          @click="save"
        >
          保存
        </button>
      </div>
    </div>
  </div>
</template>
