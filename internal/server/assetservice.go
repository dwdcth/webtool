// Package server 提供 /p/<token>/... 的插件资源服务。
// 挂载方式：Wails3 ServiceOptions.Route = "/p/"（AssetServer 会对本服务
// 做 StripPrefix，并优先于静态资源匹配）。旧版 FileLoader 的路径穿越
// 问题在此修复：所有文件路径经 Clean + Rel 双重校验。
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

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
	bridgeVer   string
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

	// 上游代理：/p/<token>/__wt__/upstream?url=<绝对URL>
	// 供需直连外部 API 但被 CORS 拦截的插件（如 Bitwarden）使用，
	// 目标 host 必须命中 manifest.webtool.upstreamHosts 白名单。
	if strings.HasPrefix(rest, "__wt__/upstream") {
		s.serveUpstream(w, r, pluginID)
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
	inject := func(b []byte) []byte { return injectBridge(b, mf.UpstreamHosts, s.bridgeVersion()) }
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

// bridgeTag 返回带内容版本的桥脚本标签（ver 为空则退回无查询形式）。
func bridgeTag(ver string) string {
	if ver == "" {
		return bridgeScriptTag
	}
	return `<script src="/host/bridge.js?v=` + ver + `"></script>`
}

var headOpenRe = regexp.MustCompile(`(?i)<head[^>]*>`)

// SetBridgeVersion 设置桥脚本内容版本（bridge.js 的内容摘要），
// 注入标签引用 ?v=<版本>，宿主升级后 URL 变化使 webview 旧缓存失效。
func (s *AssetService) SetBridgeVersion(ver string) {
	s.mu.Lock()
	s.bridgeVer = ver
	s.mu.Unlock()
}

// bridgeVersion 读取当前桥脚本版本（未设置为空串）。
func (s *AssetService) bridgeVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bridgeVer
}

// corsHeaders 为插件资源响应设置跨域放行头。
func corsHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
}

// upstreamClient 代理转发专用：60s 总超时；不跟随重定向，
// 3xx 与 Location 原样透传给插件自行处理。
// 代理探测链：环境变量（HTTPS_PROXY 等）→ 桌面系统代理（GNOME/KDE
// manual 配置）→ 直连。自托管服务器常在境外，桌面浏览器走本地代理
// 可达而 Go 直连 TLS 握手超时，跟随系统代理与浏览器行为对齐。
var upstreamClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: func() *http.Transport {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = upstreamProxyFunc
		t.TLSHandshakeTimeout = 15 * time.Second
		return t
	}(),
}

// upstreamProxyOnce 保证桌面系统代理只探测一次（exec gsettings/读配置有开销）。
var (
	upstreamProxyOnce sync.Once
	upstreamDesktop   string
)

// upstreamProxyFunc 上游转发的代理决策：环境变量显式配置优先，
// 其次进程启动后首次转发时探测到的桌面系统代理，均无则直连。
func upstreamProxyFunc(req *http.Request) (*url.URL, error) {
	if u, err := http.ProxyFromEnvironment(req); u != nil || err != nil {
		return u, err
	}
	upstreamProxyOnce.Do(func() { upstreamDesktop = detectDesktopProxy() })
	if upstreamDesktop != "" {
		return url.Parse(upstreamDesktop)
	}
	return nil, nil
}

// detectDesktopProxy 探测桌面环境的手动代理配置，返回 http://host:port
// 形式代理地址；未配置/探测失败返回空串（直连）。
func detectDesktopProxy() string {
	if p := gnomeProxy(); p != "" {
		return p
	}
	return kdeProxy()
}

// gnomeProxy 读 GNOME gsettings 的 manual 代理（https 优先，缺失回落 http）。
func gnomeProxy() string {
	out, err := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output()
	if err != nil || strings.TrimSpace(string(out)) != "'manual'" {
		return ""
	}
	for _, schema := range []string{"https", "http"} {
		h, err1 := exec.Command("gsettings", "get", "org.gnome.system.proxy."+schema, "host").Output()
		p, err2 := exec.Command("gsettings", "get", "org.gnome.system.proxy."+schema, "port").Output()
		if err1 != nil || err2 != nil {
			continue
		}
		host := strings.Trim(strings.TrimSpace(string(h)), "'")
		port := strings.TrimSpace(string(p))
		if host == "" || port == "" || port == "0" {
			continue
		}
		if n, err := strconv.Atoi(port); err == nil && n > 0 && n < 65536 {
			return "http://" + net.JoinHostPort(host, port)
		}
	}
	return ""
}

// kdeProxy 读 ~/.config/kioslaverc [Proxy Settings]（ProxyType=1 为手动）。
// 代理值兼容两种格式：http://host:port 与老式 "host port" 空格分隔。
func kdeProxy() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "kioslaverc"))
	if err != nil {
		return ""
	}
	return parseKdeProxy(string(data))
}

// parseKdeProxy 从 kioslaverc 内容提取手动代理（httpsProxy 优先，回落 httpProxy）。
func parseKdeProxy(content string) string {
	var proxyType int
	vals := map[string]string{}
	inSection := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			inSection = line == "[Proxy Settings]"
		case inSection && strings.Contains(line, "="):
			k, v, _ := strings.Cut(line, "=")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k == "ProxyType" {
				proxyType, _ = strconv.Atoi(v)
			}
			vals[k] = v
		}
	}
	if proxyType != 1 {
		return ""
	}
	for _, key := range []string{"httpsProxy", "httpProxy"} {
		v := strings.TrimSpace(strings.SplitN(vals[key], ",", 2)[0]) // 可能配多行，取第一个
		if v == "" {
			continue
		}
		if strings.Contains(v, "://") {
			return v
		}
		// 老式 "host port" 空格分隔
		fields := strings.Fields(v)
		if len(fields) == 2 {
			if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 && n < 65536 {
				return "http://" + net.JoinHostPort(fields[0], fields[1])
			}
		}
	}
	return ""
}

// upstreamAllowHeaders 允许透传给上游的请求头白名单。
// 白名单制而非全量透传：避免插件侧 Cookie/trace 头意外泄漏给第三方，
// 同时保住 Bitwarden 等客户端必需的鉴权与设备标识头。
var upstreamAllowHeaders = []string{
	"Content-Type", "Authorization", "Accept", "Accept-Language", "Accept-Encoding",
	"Bit-Client-Name", "Bit-Client-Version", "Auth-Request", "Device-Type",
	"Pragma", "Cache-Control",
}

// upstreamMaxBody 代理响应体拷贝上限（64MB），防止超大响应拖垮宿主。
const upstreamMaxBody = 64 << 20

// serveUpstream 把插件请求代理转发到白名单内的上游 API。
// GET/POST/PUT/DELETE/PATCH 均支持；OPTIONS 预检由顶部统一 CORS 头处理。
func (s *AssetService) serveUpstream(w http.ResponseWriter, r *http.Request, pluginID string) {
	rawURL := r.URL.Query().Get("url")
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "bad upstream url", http.StatusBadRequest)
		return
	}
	mf, err := s.mgr.Manifest(pluginID)
	if err != nil {
		http.Error(w, "manifest error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !matchUpstreamHost(mf.UpstreamHosts, u.Hostname()) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"upstream host not allowed"}`))
		return
	}

	// 重建出站请求：方法与 body 原样，仅透传白名单头
	out, err := http.NewRequestWithContext(r.Context(), r.Method, rawURL, r.Body)
	if err != nil {
		http.Error(w, "bad upstream request: "+err.Error(), http.StatusBadRequest)
		return
	}
	for _, h := range upstreamAllowHeaders {
		if v := r.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}
	// 透传定长 body：不定长会走 chunked 编码，部分 API 网关会拒绝
	if n := r.ContentLength; n > 0 {
		out.ContentLength = n
	}

	resp, err := upstreamClient.Do(out)
	if err != nil {
		http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 回写响应：状态码 + 少量必要头（Content-Encoding 随 Accept-Encoding
	// 透传需要一并回写，否则压缩 body 会被插件按明文解析）
	h := w.Header()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		h.Set("Content-Type", ct)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		h.Set("Content-Encoding", ce)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		h.Set("Location", loc)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		h.Set("Retry-After", ra)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, io.LimitReader(resp.Body, upstreamMaxBody)) //nolint:gosec // 上限已钳制
	}
}

// matchUpstreamHost 判断 host 是否命中 upstreamHosts 白名单。
// pattern 形如 *.example.com 时匹配任意层级子域（含 example.com 本身）：
// api.example.com / a.b.example.com / example.com 命中，
// notexample.com、api.example.com.evil.io 不命中；不含 * 时精确匹配；
// 空列表一律拒绝。
func matchUpstreamHost(patterns []string, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(patterns) == 0 {
		return false
	}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		// 显式全通配："*"（语义等同声明 http 权限，供用户自托管地址不定的插件如 Bitwarden 使用）
		if p == "*" {
			return true
		}
		if strings.HasPrefix(p, "*.") {
			// 子域通配：后缀必须落在域边界上（.example.com），
			// 域本身也算命中（与主流 cookie domain 语义一致）
			suffix := p[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) || host == p[2:] {
				return true
			}
			continue
		}
		if p == host {
			return true
		}
	}
	return false
}

// injectBridge 把宿主桥脚本注入插件 HTML（幂等）。
// upstreamHosts 非空时额外在桥脚本之前注入 __WT_UPSTREAM_HOSTS__ 变量，
// 供 bridge.js 的 fetch/XHR 包装把白名单上游请求改写到代理端点。
// ver 非空时桥脚本以 ?v=<ver> 引用（bridge.js 内容摘要）：/host/ 静态服务
// 无缓存控制头，宿主升级后若 URL 不变，webview 会继续用缓存的旧桥脚本。
func injectBridge(html []byte, upstreamHosts []string, ver string) []byte {
	upstreamTag := ""
	if len(upstreamHosts) > 0 && !bytes.Contains(html, []byte("__WT_UPSTREAM_HOSTS__")) {
		if b, err := json.Marshal(upstreamHosts); err == nil {
			upstreamTag = `<script>window.__WT_UPSTREAM_HOSTS__=` + string(b) + `;</script>`
		}
	}
	if bytes.Contains(html, []byte(`/host/bridge.js`)) {
		// 已含桥脚本：只补插 upstream 变量（找不到精确标签位置则退回常规注入点）
		if upstreamTag == "" {
			return html
		}
		if i := bytes.Index(html, []byte(bridgeScriptTag)); i >= 0 {
			var out bytes.Buffer
			out.Write(html[:i])
			out.WriteString(upstreamTag)
			out.Write(html[i:])
			return out.Bytes()
		}
	}
	inject := upstreamTag + bridgeTag(ver)
	if loc := headOpenRe.FindIndex(html); loc != nil {
		var out bytes.Buffer
		out.Write(html[:loc[1]])
		out.WriteString(inject)
		out.Write(html[loc[1]:])
		return out.Bytes()
	}
	return append([]byte(inject), html...)
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
		b = injectBridge(b, mf.UpstreamHosts, s.bridgeVersion())
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
