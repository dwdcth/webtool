package mirror

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newUpstream 起一个模拟在线工具站：首页用同源绝对路径引用资源，另有 /api/data。
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head>
<link rel="stylesheet" href="/static/app.css">
<script src="/static/app.js"></script>
</head><body>在线工具首页 <a href="/page2">下一页</a></body></html>`)
	})
	mux.HandleFunc("/static/app.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, "body{color:red}")
	})
	mux.HandleFunc("/static/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, "console.log('app')")
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body>第二页</body></html>`)
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func doGetMode(t *testing.T, m *Mirror, sub string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/p/token/"+sub, nil)
	req.URL.Path = "/" + sub
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req, sub, func(b []byte) []byte { return append(b, []byte("<!--bridge-->")...) })
	return w
}

func TestRecordThenOffline(t *testing.T) {
	up := newUpstream(t)
	dir := t.TempDir()

	// 1) 录制模式浏览首页与静态资源
	rec := New(dir, up.URL, Record)
	w := doGetMode(t, rec, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "在线工具首页") {
		t.Fatalf("首页透传失败: %d %s", w.Code, w.Body.String())
	}
	// HTML 改写：绝对路径 → 相对；桥注入
	body := w.Body.String()
	if !strings.Contains(body, `href="static/app.css"`) {
		t.Fatalf("同源绝对路径未改写: %s", body)
	}
	if !strings.Contains(body, "<!--bridge-->") {
		t.Fatal("注入回调未生效")
	}
	for _, sub := range []string{"static/app.css", "static/app.js", "page2", "api/data"} {
		if w := doGetMode(t, rec, sub); w.Code != 200 {
			t.Fatalf("录制 %s 失败: %d", sub, w.Code)
		}
	}
	if n := rec.RecordedCount(); n != 5 {
		t.Fatalf("录制条数 %d, want 5", n)
	}

	// 2) 关掉上游，离线模式应能回放全部已录资源
	up.Close()
	off := New(dir, up.URL, Offline)
	for _, sub := range []string{"", "static/app.css", "static/app.js", "page2", "api/data"} {
		w := doGetMode(t, off, sub)
		if w.Code != 200 {
			t.Fatalf("离线回放 %s 失败: %d %s", sub, w.Code, w.Body.String())
		}
	}
	if w := doGetMode(t, off, ""); !strings.Contains(w.Body.String(), "在线工具首页") {
		t.Fatal("离线首页内容不符")
	}

	// 3) 未录制路径 → 离线占位提示
	w = doGetMode(t, off, "never/recorded.js")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "未录制") {
		t.Fatalf("未录制路径应给离线占位: %d", w.Code)
	}
}

func TestRewriteHTMLKeepsCrossOrigin(t *testing.T) {
	in := []byte(`<a href="https://cdn.example.com/x.js">1</a>
<img src="//cdn.example.com/y.png">
<link href="/local.css">
<a href="/page">p</a>
<a href='rel.html'>r</a>`)
	out := string(RewriteHTML(in))
	for _, keep := range []string{
		`href="https://cdn.example.com/x.js"`,
		`src="//cdn.example.com/y.png"`,
		`href='rel.html'`,
	} {
		if !strings.Contains(out, keep) {
			t.Fatalf("不应改写跨源/相对链接 %q: %s", keep, out)
		}
	}
	for _, want := range []string{`href="local.css"`, `href="page"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("同源绝对路径应改写为 %q: %s", want, out)
		}
	}
}

func TestLivePassthroughNoRecord(t *testing.T) {
	up := newUpstream(t)
	m := New(t.TempDir(), up.URL, Live)
	if w := doGetMode(t, m, "static/app.css"); w.Code != 200 {
		t.Fatalf("live 透传失败: %d", w.Code)
	}
	if m.RecordedCount() != 0 {
		t.Fatal("live 模式不应录制")
	}
	// index.json 不应生成
	if _, err := os.Stat(filepath.Join(m.dir, "index.json")); !os.IsNotExist(err) {
		t.Fatal("live 模式不应产生录制索引")
	}
}

func TestUpstreamURLError(t *testing.T) {
	m := New(t.TempDir(), "://bad url", Live)
	w := doGetMode(t, m, "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("非法源应 502: %d", w.Code)
	}
}
