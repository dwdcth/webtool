<script setup lang="ts">
import { ref } from 'vue'
import type { Tab } from '../types'
import { PluginService } from '../../bindings/webtool/internal/hostservice'

const props = defineProps<{ tab: Tab }>()

const loading = ref(true)
const failed = ref(false)

// 同源 iframe 加载完成后注入错误拦截，转发到 Go 落盘（WebKitGTK 排障通道）
function attachErrorForwarder(frame: HTMLIFrameElement, pluginId: string) {
  try {
    const w = frame.contentWindow as (Window & typeof globalThis) | null
    if (!w) return
    const flag = w as unknown as { __wtErrHook?: boolean }
    if (flag.__wtErrHook) return
    flag.__wtErrHook = true
    const report = (kind: string, args: unknown[]) => {
      const msg = args
        .map((a) => {
          try {
            return typeof a === 'string' ? a : JSON.stringify(a)
          } catch {
            return String(a)
          }
        })
        .join(' ')
      PluginService.ReportPluginError(pluginId, `[${kind}] ${msg}`).catch(() => {})
    }
    const wc = w.console as unknown as Record<'error' | 'warn', (...a: unknown[]) => void>
    for (const m of ['error', 'warn'] as const) {
      const orig = wc[m].bind(w.console)
      wc[m] = (...args: unknown[]) => {
        report(m, args)
        orig(...args)
      }
    }
    w.addEventListener('error', (e: Event) => {
      const err = e as ErrorEvent
      report('uncaught', [`${err.message} @${(err.filename || '').split('/').pop()}:${err.lineno}`])
    })
    w.addEventListener('unhandledrejection', (e: PromiseRejectionEvent) => {
      const r: unknown = e.reason
      report(
        'rejection',
        [r instanceof Error ? `${r.message} | ${r.stack?.slice(0, 300) ?? ''}` : String(r)],
      )
    })
  } catch {
    /* 跨源（沙箱模式）时无法注入，忽略 */
  }
}

function onLoad(e: Event) {
  loading.value = false
  failed.value = false
  attachErrorForwarder(e.target as HTMLIFrameElement, props.tab.key)
}

function onError() {
  loading.value = false
  failed.value = true
}
</script>

<template>
  <div class="absolute inset-0 flex flex-col bg-[#f6f7f9] dark:bg-[#101216]">
    <!-- 承载方式：
         · 默认无 sandbox（与旧版一致）：WASM/Worker 类插件（OCR 等）
           在 opaque origin 下无法创建 Worker，兼容优先；
         · manifest 声明 webtool.sandbox=true 时启用严格沙箱。
         插件能力调用一律走 /p/<token>/api/*，Go 侧按 token 身份校验权限。 -->
    <iframe
      :src="props.tab.src"
      class="h-full w-full flex-1 border-0 bg-white"
      :sandbox="props.tab.sandbox ? 'allow-scripts allow-forms allow-popups allow-modals allow-downloads' : undefined"
      referrerpolicy="no-referrer"
      @load="onLoad"
      @error="onError"
    />

    <div
      v-if="loading"
      class="pointer-events-none absolute inset-0 flex items-center justify-center bg-[#f6f7f9] dark:bg-[#101216]"
    >
      <div class="flex flex-col items-center gap-3 text-zinc-400 dark:text-zinc-500">
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-zinc-300 border-t-zinc-500 dark:border-zinc-700 dark:border-t-zinc-400"
        />
        <span class="text-xs">正在加载 {{ props.tab.title }}…</span>
      </div>
    </div>

    <div v-if="failed" class="absolute inset-0 flex items-center justify-center">
      <div class="text-sm text-red-500 dark:text-red-400">插件加载失败（入口页不可用）</div>
    </div>
  </div>
</template>
