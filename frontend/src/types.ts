// 与 Go 侧 plugin.Info 对齐（camelCase JSON）。
export interface PluginInfo {
  id: string
  name: string
  version: string
  description: string
  author: string
  icon: string
  category: string
  keywords: string[]
  permissions: string[]
  homepage: string
  type: string // bundle | web | link（对应 Go manifest.PluginType）
  sourceUrl?: string
  mode?: string // web 插件：live | record | offline
  sandbox?: boolean // webtool.sandbox：严格沙箱 iframe
  enabled: boolean
  installedAt: string
  source: string
  entry: string
  hasInterceptors: boolean
}

export interface Tab {
  key: string // plugin id
  title: string
  src: string // iframe url
  iconEmoji?: string
  iconUrl?: string
  sandbox?: boolean // manifest 声明 webtool.sandbox=true 时启用严格沙箱
}

export interface Toast {
  id: number
  kind: 'ok' | 'err'
  text: string
}
