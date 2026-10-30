package server

import (
	"archive/zip"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtool/internal/plugin"
	"webtool/internal/pluginapi"
	"webtool/internal/router"
)

func newTestStack(t *testing.T) (*plugin.Manager, *AssetService, *router.Router) {
	t.Helper()
	dir := t.TempDir()
	m := plugin.NewManager(filepath.Join(dir, "plugins"), filepath.Join(dir, "plugins.json"))
	if err := m.LoadInstalled(); err != nil {
		t.Fatal(err)
	}
	r := router.New()
	api := pluginapi.New(m)
	return m, NewAssetService(m, r, api), r
}

// installPluginZip 构造 zip 并走真实安装路径。
func installPluginZip(t *testing.T, m *plugin.Manager, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "plugin.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := m.InstallZipPath(p, "file", ""); err != nil {
		t.Fatal(err)
	}
}

// doGet 模拟 Wails AssetServer 对 /p/ 前缀 StripPrefix 后的请求。
func doGet(svc *AssetService, strippedPath string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/p"+strippedPath, nil)
	req.URL.Path = strippedPath // StripPrefix 之后到达服务的路径
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func TestServeBundleInjectsBridge(t *testing.T) {
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"p1","unihub":{"id":"p1","entry":"index.html"}}`,
		"index.html":   `<html><head><title>t</title></head><body>hi</body></html>`,
	})
	tok := r.TokenFor("p1")
	w := doGet(svc, "/"+tok+"/index.html")

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `<script src="/host/bridge.js"></script>`) {
		t.Fatalf("bridge 未注入: %s", body)
	}
	if !strings.Contains(body, "<title>t</title>") {
		t.Fatal("原内容丢失")
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type 错误: %s", ct)
	}
}

func TestServeBundlePathTraversalBlocked(t *testing.T) {
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"p2","unihub":{"id":"p2"}}`,
		"index.html":   "<html></html>",
	})
	// 在数据目录根（插件目录之上）放敏感文件，模拟穿越目标
	secret := filepath.Join(filepath.Dir(m.PluginsDir()), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)

	tok := r.TokenFor("p2")
	for _, evil := range []string{
		"/" + tok + "/../../secret.txt",
		"/" + tok + "/a/../../../../secret.txt",
	} {
		w := doGet(svc, evil)
		if w.Code == 200 && strings.Contains(w.Body.String(), "TOP SECRET") {
			t.Fatalf("路径穿越未被拦截: %s -> %d %s", evil, w.Code, w.Body.String())
		}
		if w.Code == 403 {
			continue // 显式拒绝
		}
		// 若路径规范化后指向不存在的文件，404 也可接受，但绝不能 200 泄漏
	}
}

func TestServeUnknownToken(t *testing.T) {
	_, svc, _ := newTestStack(t)
	w := doGet(svc, "/deadbeef/index.html")
	if w.Code != 404 {
		t.Fatalf("未知 token 应 404, got %d", w.Code)
	}
}

func TestInterceptRespondOnAssetRequest(t *testing.T) {
	// 数据驱动的 chrome_tool 拦截（替代旧版硬编码 main.go hack）
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"ctool","unihub":{"id":"ctool"},
		"webtool":{"interceptors":[
			{"match":{"pathRegex":"^chrome_tool/(notice|stat)"},
			 "action":{"type":"respond","status":200,"body":"{\"code\":0,\"data\":[],\"info\":\"操作成功\"}","contentType":"application/json"}}
		]}}`,
		"index.html": "<html></html>",
	})
	tok := r.TokenFor("ctool")

	w := doGet(svc, "/"+tok+"/chrome_tool/notice")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "操作成功") {
		t.Fatalf("拦截规则未生效: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Fatalf("Content-Type 应为 json: %s", ct)
	}

	// 未命中规则的资源路径正常服务
	w2 := doGet(svc, "/"+tok+"/index.html")
	if w2.Code != 200 {
		t.Fatalf("正常资源被误拦: %d", w2.Code)
	}
}

func TestResolvePluginFile(t *testing.T) {
	dir := "/data/plugins/p"
	// path.Clean("/"+sub) 把 .. 钳制在目录内：越界输入被规整为目录内
	// 不存在的路径（→404），而非逃逸文件系统。
	cases := map[string]string{
		"index.html":          filepath.Join(dir, "index.html"),
		"assets/app.js":       filepath.Join(dir, "assets/app.js"),
		"../../../etc/passwd": filepath.Join(dir, "etc/passwd"), // 钳制，安全
		"/abs/path":           filepath.Join(dir, "abs/path"),   // 钳制，安全
		"":                    "",
	}
	for in, want := range cases {
		if got := resolvePluginFile(dir, in); got != want {
			t.Fatalf("resolvePluginFile(%q) = %q, want %q", in, got, want)
		}
	}
	// 双保险：任意输入的结果都必须落在 dir 内
	for _, in := range []string{"a/b", "..", "../..", "x/../../y", "\\"} {
		got := resolvePluginFile(dir, in)
		if got == "" {
			continue
		}
		rel, err := filepath.Rel(dir, got)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("%q 逃逸出插件目录: %q", in, got)
		}
	}
}

func TestDisabledPluginRejected(t *testing.T) {
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"off","unihub":{"id":"off"}}`,
		"index.html":   "<html></html>",
	})
	if err := m.SetEnabled("off", false); err != nil {
		t.Fatal(err)
	}
	tok := r.TokenFor("off")
	w := doGet(svc, "/"+tok+"/index.html")
	if w.Code != 503 {
		t.Fatalf("停用插件应 503, got %d", w.Code)
	}
}
