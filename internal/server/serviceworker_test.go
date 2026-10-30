package server

import (
	"strings"
	"testing"
)

// TestServiceWorkerNeutralized: 插件自带的 service worker 脚本必须被
// 替换为透传版本，否则 SW 会拦截自身作用域的导航导致 iframe 空白
// （pearocr / regex101 的实际故障）。
func TestServiceWorkerNeutralized(t *testing.T) {
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json":      `{"name":"swp","unihub":{"id":"swp"}}`,
		"index.html":        "<html></html>",
		"service-worker.js": "self.addEventListener('fetch', e => e.respondWith(caches.match(e.request)))",
		"sw.js":             "evil cache logic",
		"nested/sw.js":      "nested evil",
	})
	tok := r.TokenFor("swp")

	for _, swPath := range []string{"service-worker.js", "sw.js", "nested/sw.js"} {
		w := doGet(svc, "/"+tok+"/"+swPath)
		if w.Code != 200 {
			t.Fatalf("%s 应 200, got %d", swPath, w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, "caches.match") || strings.Contains(body, "evil") {
			t.Fatalf("%s 未被中和: %s", swPath, body[:min(120, len(body))])
		}
		if !strings.Contains(body, "pass-through") {
			t.Fatalf("%s 应为 no-op SW: %s", swPath, body[:min(120, len(body))])
		}
		if w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s 应带 no-cache 以触发 SW 更新", swPath)
		}
	}

	// 普通脚本不受影响
	w := doGet(svc, "/"+tok+"/index.html")
	if !strings.Contains(w.Body.String(), "/host/bridge.js") {
		t.Fatal("正常 HTML 服务受影响")
	}
}
