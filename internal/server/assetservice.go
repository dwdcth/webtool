// Package server 提供 /p/<token>/... 的插件资源服务。
// 挂载方式：Wails3 ServiceOptions.Route = "/p/"（AssetServer 会对本服务
// 做 StripPrefix，并优先于静态资源匹配）。旧版 FileLoader 的路径穿越
// 问题在此修复：所有文件路径经 Clean + Rel 双重校验。
package server

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"webtool/internal/intercept"
	"webtool/internal/manifest"
	"webtool/internal/mirror"
	"webtool/internal/plugin"
	"webtool/internal/pluginapi"
	"webtool/internal/router"
)

type AssetService struct {
	mgr *plugin.Manager
	rtr *router.Router
	api *pluginapi.API

	mu          sync.RWMutex
	globalRules []intercept.Rule
	mirrors     map[string]*mirror.Mirror
}

func NewAssetService(mgr *plugin.Manager, rtr *router.Router, api *pluginapi.API) *AssetService {
	return &AssetService{mgr: mgr, rtr: rtr, api: api}
}

func (s *AssetService) ServiceName() string { return "webtool.assets" }

func (s *AssetService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 全部 /p/ 响应放行 CORS：插件可能运行在沙箱 iframe（opaque origin），
	// 其 fetch/XHR 需要跨域授权才能加载同服务器的资源（模型/wasm 等）。
	corsHeaders(w)

	// 到达这里的 path 形如 /<token>/<rest>
	p := strings.TrimPrefix(r.URL.Path, "/")
	token, rest, _ := strings.Cut(p, "/")
	if token == "" || rest == "" && token != "" && p == "" {
		http.NotFound(w, r)
		return
	}
	pluginID, ok := s.rtr.PluginOf(token)
	if !ok {
		http.Error(w, "unknown plugin token", http.StatusNotFound)
		return
	}

	// 能力 API：/p/<token>/__wt__/api/<domain>/<method>
	// 前缀 __wt__ 刻意隐蔽：被镜像的网站常请求 /api/... 之类的绝对路径，
	// 经 Referer 回填后落在 /p/<token>/api/...，不能与能力 API 撞车。
	if strings.HasPrefix(rest, "__wt__/api/") {
		sub := strings.TrimPrefix(rest, "__wt__/api/")
		domain, method, found := strings.Cut(sub, "/")
		if !found || domain == "" || method == "" {
			http.Error(w, "bad api path", http.StatusNotFound)
			return
		}
		s.api.Handle(w, r, pluginID, domain, method)
		return
	}

	info, err := s.mgr.Get(pluginID)
	if err != nil {
		http.Error(w, "plugin not available: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if !info.Enabled {
		http.Error(w, "plugin disabled", http.StatusServiceUnavailable)
		return
	}
	mf, err := s.mgr.Manifest(pluginID)
	if err != nil {
		http.Error(w, "manifest error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 拦截规则：插件自身规则优先，其次全局规则（rules.json）
	reqPath := "/" + rest
	rules := mf.Interceptors
	s.mu.RLock()
	rules = append(append([]intercept.Rule{}, mf.Interceptors...), s.globalRules...)
	s.mu.RUnlock()
	if rule := intercept.Evaluate(rules, r.Host, reqPath); rule != nil {
		switch rule.Action.Type {
		case intercept.ActionBlock, intercept.ActionRespond:
			intercept.Apply(w, rule.Action)
			return
		case intercept.ActionRewrite:
			rest = strings.TrimPrefix(intercept.RewritePath(rule, reqPath), "/")
		default:
			// pass / cache-first / no-cache：P2 镜像代理消费，此处继续
		}
	}

	switch mf.Type {
	case manifest.Bundle:
		s.serveBundle(w, r, mf, rest)
	case manifest.Web, manifest.Link:
		s.serveWeb(w, r, mf, rest)
	default:
		http.Error(w, "unknown plugin type", http.StatusInternalServerError)
	}
}

// serveWeb 承载 web（镜像代理）与 link（纯透传）插件。
func (s *AssetService) serveWeb(w http.ResponseWriter, r *http.Request, mf *manifest.Manifest, sub string) {
	if mf.SourceURL == "" {
		http.Error(w, "web 插件缺少 sourceUrl", http.StatusServiceUnavailable)
		return
	}
	mode := mirror.Mode(mf.Mode)
	if mode == "" || mf.Type == manifest.Link {
		mode = mirror.Live
	}

	m := s.mirrorFor(mf, mode)
	inject := func(b []byte) []byte { return injectBridge(b) }
	m.ServeHTTP(w, r, sub, inject)
}

// mirrorFor 返回插件的镜像实例（缓存；模式变化时由 InvalidateMirror 重建）。
func (s *AssetService) mirrorFor(mf *manifest.Manifest, mode mirror.Mode) *mirror.Mirror {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := mf.ID + "\x00" + string(mode)
	if m, ok := s.mirrors[key]; ok {
		return m
	}
	m := mirror.New(mf.Dir, mf.SourceURL, mode)
	if s.mirrors == nil {
		s.mirrors = map[string]*mirror.Mirror{}
	}
	s.mirrors[key] = m
	return m
}

// InvalidateMirrors 清除插件全部镜像缓存（模式切换后调用）。
func (s *AssetService) InvalidateMirrors(pluginID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.mirrors {
		if strings.HasPrefix(k, pluginID+"\x00") {
			delete(s.mirrors, k)
		}
	}
}

// mimeOverrides 补齐 WebKit 严格要求的常见类型。
var mimeOverrides = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".wasm":  "application/wasm",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".map":   "application/json",
}

// hashFileRe 匹配带内容摘要的静态资源文件名（vite/webpack 产物）。
var hashFileRe = regexp.MustCompile(`[.-][0-9a-fA-F]{8,}\.[a-zA-Z0-9]+$`)

const bridgeScriptTag = `<script src="/host/bridge.js"></script>`

var headOpenRe = regexp.MustCompile(`(?i)<head[^>]*>`)

// corsHeaders 为插件资源响应设置跨域放行头。
func corsHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
}

// injectBridge 把宿主桥脚本注入插件 HTML（幂等）。
func injectBridge(html []byte) []byte {
	if bytes.Contains(html, []byte(`/host/bridge.js`)) {
		return html
	}
	if loc := headOpenRe.FindIndex(html); loc != nil {
		var out bytes.Buffer
		out.Write(html[:loc[1]])
		out.WriteString(bridgeScriptTag)
		out.Write(html[loc[1]:])
		return out.Bytes()
	}
	return append([]byte(bridgeScriptTag), html...)
}

// resolvePluginFile 把请求子路径安全映射到插件目录内文件。
// 返回空串表示路径越界（403）。
func resolvePluginFile(dir, sub string) string {
	clean := path.Clean("/" + sub) // 恒以 / 开头且不含 ..
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" {
		return ""
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	relChecked, err := filepath.Rel(dir, full)
	if err != nil || relChecked == ".." || strings.HasPrefix(relChecked, ".."+string(filepath.Separator)) {
		return ""
	}
	return full
}

// noopServiceWorker 是替换插件自带 service worker 的透传实现。
// 插件是本地静态快照，workbox 类 SW 的预缓存/拦截在代理路径下会失败，
// 典型症状：SW 注册成功后拦截自身导航 → iframe 空白（pearocr/regex101）。
// 透传 SW 不响应 fetch 事件，请求全部回落到网络（即本地资产服务）。
const noopServiceWorker = `// webtool: plugin service workers are neutralized (pass-through)
self.addEventListener('install', function (e) { self.skipWaiting(); });
self.addEventListener('activate', function (e) { e.waitUntil(self.clients.claim()); });
self.addEventListener('fetch', function (e) { /* pass-through */ });
`

// isServiceWorkerScript 判断请求是否指向插件自带的 SW 脚本。
func isServiceWorkerScript(sub string) bool {
	base := path.Base(path.Clean("/" + sub))
	return base == "service-worker.js" || base == "sw.js"
}

func (s *AssetService) serveBundle(w http.ResponseWriter, r *http.Request, mf *manifest.Manifest, sub string) {
	if sub == "" {
		sub = mf.Entry // 打开插件根 → 入口页
	}
	full := resolvePluginFile(mf.Dir, sub)
	if full == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		// 目录请求兼容：/<子路径>/ → 其下 index.html
		indexPath := filepath.Join(full, "index.html")
		if st2, err2 := os.Stat(indexPath); err2 == nil && !st2.IsDir() {
			full, st = indexPath, st2
		} else {
			http.NotFound(w, r)
			return
		}
	}
	if st.IsDir() {
		indexPath := filepath.Join(full, "index.html")
		st2, err2 := os.Stat(indexPath)
		if err2 != nil || st2.IsDir() {
			http.NotFound(w, r)
			return
		}
		full, st = indexPath, st2
	}

	ext := strings.ToLower(filepath.Ext(full))

	// 中和插件自带的 service worker（见 noopServiceWorker 注释）
	if isServiceWorkerScript(sub) {
		w.Header().Set("Content-Type", mimeOverrides[".js"])
		// no-cache：让浏览器每次导航都重新拉取本文件，已注册的旧 SW 会被更新
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(noopServiceWorker))
		}
		return
	}

	if ext == ".html" || ext == ".htm" {
		b, err := os.ReadFile(full) //nolint:gosec // 路径已做越界校验
		if err != nil {
			http.Error(w, "read error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		b = injectBridge(b)
		w.Header().Set("Content-Type", mimeOverrides[".html"])
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(b)
		}
		return
	}

	f, err := os.Open(full) //nolint:gosec // 路径已做越界校验
	if err != nil {
		http.Error(w, "open error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	ctype, ok := mimeOverrides[ext]
	if !ok {
		ctype = mime.TypeByExtension(ext)
		if ctype == "" {
			ctype = "application/octet-stream"
		}
	}
	w.Header().Set("Content-Type", ctype)
	if hashFileRe.MatchString(filepath.Base(full)) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

var _ = fmt.Sprintf // 保留 fmt 依赖（错误信息格式化）
