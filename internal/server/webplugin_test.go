package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"webtool/internal/manifest"
	"webtool/internal/mirror"
	"webtool/internal/plugin"
	"webtool/internal/pluginapi"
	"webtool/internal/router"
)

// newWebStack 构造带本地上游站的完整栈。
func newWebStack(t *testing.T) (*plugin.Manager, *AssetService, *router.Router, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html><head><script src=\"/app.js\"></script></head><body>上游页 %s</body></html>", r.URL.Path)
	}))
	t.Cleanup(up.Close)

	dir := t.TempDir()
	m := plugin.NewManager(filepath.Join(dir, "plugins"), filepath.Join(dir, "plugins.json"))
	if err := m.LoadInstalled(); err != nil {
		t.Fatal(err)
	}
	r := router.New()
	api := pluginapi.New(m)
	svc := NewAssetService(m, r, api)
	return m, svc, r, up
}

func createWebPlugin(t *testing.T, m *plugin.Manager, upURL, mode string) {
	t.Helper()
	_, err := m.CreateFromManifest(&manifest.Manifest{
		ID: "site-test", Name: "SiteTest", Type: manifest.Web,
		SourceURL: upURL, Mode: mode, Category: "web",
	}, "capture", upURL)
	if err != nil {
		t.Fatal(err)
	}
}

// TestWebPluginRecordAndOffline: 走 AssetService 的 web 插件全链路——
// 录制浏览 → 关上游 → 固化离线仍可打开。
func TestWebPluginRecordAndOffline(t *testing.T) {
	m, svc, r, up := newWebStack(t)
	createWebPlugin(t, m, up.URL, "record")
	tok := r.TokenFor("site-test")

	// 录制模式：首页 + 绝对路径资源
	w := doGet(svc, "/"+tok+"/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "上游页 /") {
		t.Fatalf("web 插件首页失败: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `src="app.js"`) {
		t.Fatalf("绝对路径未改写: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/host/bridge.js") {
		t.Fatal("桥未注入")
	}
	w2 := doGet(svc, "/"+tok+"/app.js")
	if w2.Code != 200 {
		t.Fatalf("资源代理失败: %d", w2.Code)
	}

	// 关闭上游 → 切离线 → 仍可完整打开
	up.Close()
	if err := m.UpdateWebMode("site-test", "offline"); err != nil {
		t.Fatal(err)
	}
	svc.InvalidateMirrors("site-test")

	w3 := doGet(svc, "/"+tok+"/")
	if w3.Code != 200 || !strings.Contains(w3.Body.String(), "上游页 /") {
		t.Fatalf("离线回放失败: %d %s", w3.Code, w3.Body.String())
	}
	w4 := doGet(svc, "/"+tok+"/app.js")
	if w4.Code != 200 {
		t.Fatalf("离线资源失败: %d", w4.Code)
	}
	if h := w4.Header().Get("X-Webtool-Mirror"); h != "offline" {
		t.Fatalf("应标记 offline: %s", h)
	}
}

// TestCapabilityAPIPrefix: 能力 API 走 __wt__ 前缀，且网站的 /api 请求
// 不会被误当能力调用（撞车回归）。
func TestCapabilityAPIPrefix(t *testing.T) {
	m, svc, r, up := newWebStack(t)
	createWebPlugin(t, m, up.URL, "record")
	tok := r.TokenFor("site-test")

	// 能力 API 正常工作（storage.get）
	req := httptest.NewRequest("POST", "/p/"+tok+"/__wt__/api/storage/get", strings.NewReader(`{"key":"x"}`))
	req.URL.Path = "/" + tok + "/__wt__/api/storage/get"
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("能力 API 失败: %d %s", w.Code, w.Body.String())
	}
	// CORS 头存在（沙箱/跨源 fetch 需要）
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("缺少 CORS 头")
	}

	// 网站自身的 /api/data 请求 → 交给镜像代理回源，而不是能力 API
	w2 := doGet(svc, "/"+tok+"/api/data")
	if w2.Code == 200 && strings.Contains(w2.Body.String(), `"ok":true`) && strings.Contains(w2.Header().Get("Content-Type"), "application/json") && w2.Header().Get("X-Webtool-Mirror") == "" {
		t.Fatal("/api/data 被误判为能力调用")
	}
	if w2.Header().Get("X-Webtool-Mirror") != "record" {
		t.Fatalf("/api/data 应走镜像: %s", w2.Body.String())
	}
}

// TestCreateFromManifestPersists: 编程创建的 web 插件重启后仍可加载。
func TestCreateFromManifestPersists(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	regPath := filepath.Join(dir, "plugins.json")

	m1 := plugin.NewManager(pluginsDir, regPath)
	createWebPlugin(t, m1, "https://example.com/", "record")

	// 模拟重启：新 Manager 实例
	m2 := plugin.NewManager(pluginsDir, regPath)
	if err := m2.LoadInstalled(); err != nil {
		t.Fatal(err)
	}
	info, err := m2.Get("site-test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != manifest.Web || info.Mode != "record" || info.SourceURL != "https://example.com/" {
		t.Fatalf("重启后元数据不符: %+v", info)
	}
}

// TestMirrorModeDefaultLive: mode 缺省按 live 处理。
func TestMirrorModeDefaultLive(t *testing.T) {
	m, svc, r, up := newWebStack(t)
	createWebPlugin(t, m, up.URL, "")
	_ = mirror.Live
	tok := r.TokenFor("site-test")
	w := doGet(svc, "/"+tok+"/")
	if w.Code != 200 || w.Header().Get("X-Webtool-Mirror") != "live" {
		t.Fatalf("缺省模式应为 live: %d %s", w.Code, w.Header().Get("X-Webtool-Mirror"))
	}
}
