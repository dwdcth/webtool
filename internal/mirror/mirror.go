// Package mirror 实现网页插件的"回源代理 + 录制 + 离线回放"。
//
// 三种模式：
//   - live    在线浏览：请求映射回源站透传
//   - record  录制：透传同时把响应落盘（mirror/files/ + index.json）
//   - offline 离线（已固化）：优先本地缓存；未命中尝试回源，不通则占位提示
//
// 请求映射：/p/<token>/<sub>  →  <sourceURL>/<sub>（query 原样带过去）。
// HTML 响应做两步改写：把同源绝对路径属性（src="/x"）改为相对（"x"），
// 使后续请求留在 /p/<token>/ 命名空间（配合宿主的 Referer 回填中间件，
// JS 里 fetch('/api') 这类动态请求也能回到本代理）。
package mirror

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Mode string

const (
	Live    Mode = "live"
	Record  Mode = "record"
	Offline Mode = "offline"
)

const (
	maxRecordBytes = 32 << 20 // 单响应录制上限
	maxBodyBytes   = 64 << 20 // 透传读取上限
)

type entry struct {
	File        string `json:"file"`
	ContentType string `json:"contentType,omitempty"`
	Status      int    `json:"status"`
}

type Mirror struct {
	mu       sync.Mutex
	dir      string // 插件镜像目录（mirror/）
	source   *url.URL
	mode     Mode
	client   *http.Client
	index    map[string]entry
	fileSeq  int
	inited   bool
	initErr  error
	initOnce sync.Once
}

func New(pluginDir, sourceURL string, mode Mode) *Mirror {
	m := &Mirror{
		dir:   filepath.Join(pluginDir, "mirror"),
		mode:  mode,
		index: map[string]entry{},
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	if u, err := url.Parse(sourceURL); err == nil && u.Scheme != "" && u.Host != "" {
		m.source = u
	}
	return m
}

func (m *Mirror) Mode() Mode { return m.mode }

// lazyInit 加载录制索引（首次使用时）。
func (m *Mirror) lazyInit() {
	m.initOnce.Do(func() {
		m.inited = true
		b, err := os.ReadFile(filepath.Join(m.dir, "index.json"))
		if err != nil {
			return
		}
		var idx map[string]entry
		if jsonUnmarshal(b, &idx) == nil && idx != nil {
			m.index = idx
		}
	})
}

// upstreamURL 把命名空间子路径映射回源站地址。
func (m *Mirror) upstreamURL(sub, rawQuery string) (string, error) {
	if m.source == nil {
		return "", fmt.Errorf("镜像源地址无效")
	}
	base := *m.source
	// 源 base 路径 + 子路径（sub 为命名空间根下的相对路径）
	basePath := strings.TrimSuffix(base.Path, "/")
	if sub != "" {
		basePath += "/" + strings.TrimPrefix(sub, "/")
	}
	out := base
	out.Path = basePath
	out.RawQuery = rawQuery
	return out.String(), nil
}

// ServeHTTP 处理一次镜像请求。sub 不含 query；rewrite 注入桥脚本。
func (m *Mirror) ServeHTTP(w http.ResponseWriter, r *http.Request, sub string, inject func([]byte) []byte) {
	m.lazyInit()

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// 非读请求：只透传不录制
		m.proxy(w, r, sub, false, inject)
		return
	}

	key := "/" + sub
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}

	// 离线模式：优先本地
	if m.mode == Offline {
		if m.serveCached(w, r, key, inject) {
			return
		}
		// 未命中：尝试回源（可能在线），失败则占位提示
		m.proxy(w, r, sub, false, inject)
		return
	}

	// live / record：透传（record 落盘）
	m.proxy(w, r, sub, m.mode == Record, inject)
}

func (m *Mirror) serveCached(w http.ResponseWriter, r *http.Request, key string, inject func([]byte) []byte) bool {
	m.mu.Lock()
	e, ok := m.index[key]
	m.mu.Unlock()
	if !ok {
		return false
	}
	b, err := os.ReadFile(filepath.Join(m.dir, "files", e.File)) //nolint:gosec // 文件名来自自建索引
	if err != nil {
		return false
	}
	if e.ContentType != "" {
		w.Header().Set("Content-Type", e.ContentType)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Webtool-Mirror", "offline")
	if isHTML(e.ContentType) && inject != nil {
		b = inject(b)
	}
	w.WriteHeader(firstNonZero(e.Status, 200))
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
	return true
}

func (m *Mirror) proxy(w http.ResponseWriter, r *http.Request, sub string, record bool, inject func([]byte) []byte) {
	upstream, err := m.upstreamURL(sub, r.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req, err := http.NewRequest(r.Method, upstream, nil)
	if err != nil {
		http.Error(w, "bad upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	// 转发有意义的头
	for _, h := range []string{"Accept", "Accept-Language", "Content-Type", "User-Agent"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := m.client.Do(req)
	if err != nil {
		if m.mode == Offline {
			m.serveOfflineMiss(w, sub)
			return
		}
		http.Error(w, "回源失败（离线且未录制该资源）: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "读取上游响应失败: "+err.Error(), http.StatusBadGateway)
		return
	}

	if isHTML(ct) && inject != nil {
		body = inject(RewriteHTML(body))
	}

	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	for _, h := range []string{"Location"} { // 少量透传头
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Webtool-Mirror", string(m.mode))
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}

	if record && resp.StatusCode == 200 && len(body) <= maxRecordBytes && isRecordable(ct) {
		m.store(sub, r.URL.RawQuery, ct, resp.StatusCode, body)
	}
}

func (m *Mirror) store(sub, rawQuery, ct string, status int, body []byte) {
	key := "/" + sub
	if rawQuery != "" {
		key += "?" + rawQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.index[key]; exists {
		return // 首次录制为准
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "files"), 0o755); err != nil {
		return
	}
	m.fileSeq++
	file := fmt.Sprintf("%06d.dat", m.fileSeq)
	if err := os.WriteFile(filepath.Join(m.dir, "files", file), body, 0o644); err != nil {
		return
	}
	m.index[key] = entry{File: file, ContentType: ct, Status: status}
	if b, err := jsonMarshalIndent(m.index); err == nil {
		_ = os.WriteFile(filepath.Join(m.dir, "index.json"), b, 0o644)
	}
}

func (m *Mirror) serveOfflineMiss(w http.ResponseWriter, sub string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>离线未命中</title></head>
<body style="font:14px sans-serif;padding:32px;color:#555">
<b>离线模式：该资源未录制</b><p>` + htmlEscape(sub) + `</p>
<p>切回在线/录制模式访问一次该页面即可补录。</p></body></html>`))
}

// RecordedCount 返回已录制条目数（UI 展示用）。
func (m *Mirror) RecordedCount() int {
	m.lazyInit()
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.index)
}

// ---- HTML 改写 ----

// absAttrRe 匹配携带绝对路径值的属性（src|href|action|poster="/x…"）。
// 捕获组：1=属性名+引号，2=以 / 开头的值；尾部引号不消费。
// 协议相对（//cdn…）在回调中跳过（RE2 无前瞻/反向引用）。
var absAttrRe = regexp.MustCompile(`((?:src|href|action|poster|data-src)\s*=\s*["'])(/[^"'\s>]*)`)

// RewriteHTML 把同源绝对路径改成相对路径，让请求留在命名空间内。
// 跨源（//cdn、https://）与协议相对链接不动。
func RewriteHTML(html []byte) []byte {
	out := absAttrRe.ReplaceAllFunc(html, func(match []byte) []byte {
		m := absAttrRe.FindSubmatch(match)
		value := string(m[2])
		if strings.HasPrefix(value, "//") {
			return match // 协议相对链接，保持原样
		}
		// "/x" → "x"（相对当前命名空间根）
		return append(append([]byte{}, m[1]...), strings.TrimPrefix(value, "/")...)
	})
	return out
}

// ---- 小工具 ----

func isHTML(ct string) bool {
	return strings.HasPrefix(ct, "text/html")
}

// isRecordable 限定可安全落盘并回放的类型（HTML/文本/图片/字体等，不含视频）。
func isRecordable(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch {
	case ct == "":
		return true
	case strings.HasPrefix(ct, "text/"), strings.HasPrefix(ct, "application/"):
		return true
	case strings.HasPrefix(ct, "image/"), strings.HasPrefix(ct, "font/"):
		return true
	case strings.HasPrefix(ct, "audio/"), strings.HasPrefix(ct, "video/"):
		return false // 媒体太大，不录制
	}
	return false
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}
