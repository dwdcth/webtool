package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMatchUpstreamHost(t *testing.T) {
	wildcard := []string{"*.bitwarden.com"}
	// *.bitwarden.com：任意层级子域 + 域本身命中
	for _, host := range []string{"api.bitwarden.com", "bitwarden.com", "a.b.bitwarden.com"} {
		if !matchUpstreamHost(wildcard, host) {
			t.Fatalf("*.bitwarden.com 应命中 %q", host)
		}
	}
	// 域边界校验：前缀粘连 / 后缀拼接 / 同名不同域都拒绝
	for _, host := range []string{"notbitwarden.com", "api.bitwarden.com.evil.io", "bitwarden.eu"} {
		if matchUpstreamHost(wildcard, host) {
			t.Fatalf("*.bitwarden.com 不应命中 %q", host)
		}
	}
	// 精确 pattern：完全相等才命中
	exact := []string{"vault.example.org"}
	if !matchUpstreamHost(exact, "vault.example.org") {
		t.Fatal("精确 pattern 应命中自身")
	}
	if matchUpstreamHost(exact, "x.vault.example.org") {
		t.Fatal("精确 pattern 不应命中子域")
	}
	// 空列表一律拒绝
	if matchUpstreamHost(nil, "bitwarden.com") || matchUpstreamHost([]string{}, "bitwarden.com") {
		t.Fatal("空 patterns 应拒绝")
	}
}

func TestParseKdeProxy(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		{
			name:  "手动 https 优先",
			want:  "http://127.0.0.1:7897",
			content: "[Proxy Settings]\nProxyType=1\nhttpProxy=http://127.0.0.1 7890\nhttpsProxy=http://127.0.0.1:7897\n",
		},
		{
			name:  "手动老式空格分隔",
			want:  "http://127.0.0.1:7890",
			content: "[Proxy Settings]\nProxyType=1\nhttpProxy=127.0.0.1 7890\n",
		},
		{
			name:  "非手动类型忽略",
			want:  "",
			content: "[Proxy Settings]\nProxyType=0\nhttpsProxy=http://127.0.0.1:7897\n",
		},
		{
			name:  "无配置节忽略",
			want:  "",
			content: "[Other]\nProxyType=1\nhttpsProxy=http://127.0.0.1:7897\n",
		},
		{
			name:  "非法端口忽略",
			want:  "",
			content: "[Proxy Settings]\nProxyType=1\nhttpsProxy=127.0.0.1 abc\n",
		},
	}
	for _, c := range cases {
		if got := parseKdeProxy(c.content); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// doUpstreamReq 模拟 StripPrefix 后的插件请求（支持方法/query/body/头）。
func doUpstreamReq(svc *AssetService, method, strippedTarget string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/p"+strippedTarget, body)
	u, err := url.Parse(strippedTarget)
	if err != nil {
		panic(err)
	}
	req.URL.Path = u.Path // 与 Wails StripPrefix 行为对齐（query 不动）
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func TestMatchUpstreamHostWildcardAll(t *testing.T) {
	// "*" 全通配：任意 host 放行（等同 http 权限语义）
	if !matchUpstreamHost([]string{"*"}, "any.example.com") {
		t.Fatal("* 应匹配任意域名")
	}
	if !matchUpstreamHost([]string{"*.bitwarden.com", "*"}, "vault.mysite.cn") {
		t.Fatal("含 * 的列表应放行非白名单域")
	}
}

func TestUpstreamProxyEndToEnd(t *testing.T) {
	// httptest 上游：回显方法、body 与白名单头
	var gotMethod, gotPath, gotBody, gotCT, gotAuth, gotEvil string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(b)
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotEvil = r.Header.Get("X-Evil")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"upstream":true}`))
	}))
	defer up.Close()

	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"bwd","unihub":{"id":"bwd"},
			"webtool":{"upstreamHosts":["*.bitwarden.com","127.0.0.1"]}}`,
		"index.html": `<html><head><title>bwd</title></head><body>hi</body></html>`,
	})
	tok := r.TokenFor("bwd")

	// POST JSON 经 /p/<token>/__wt__/upstream 透传到上游
	target := "/" + tok + "/__wt__/upstream?" + url.Values{"url": {up.URL + "/echo"}}.Encode()
	body := `{"email":"a@b.c"}`
	w := doUpstreamReq(svc, "POST", target, strings.NewReader(body), map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer tok123",
		"X-Evil":        "should-not-forward",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("代理应 200, got %d body=%s", w.Code, w.Body.String())
	}
	if gotMethod != "POST" || gotPath != "/echo" {
		t.Fatalf("上游收到的请求不对: method=%s path=%s", gotMethod, gotPath)
	}
	if gotBody != body {
		t.Fatalf("body 未透传: got %q want %q", gotBody, body)
	}
	if gotCT != "application/json" || gotAuth != "Bearer tok123" {
		t.Fatalf("白名单头未透传: ct=%q auth=%q", gotCT, gotAuth)
	}
	if gotEvil != "" {
		t.Fatalf("白名单外的头不应透传: %q", gotEvil)
	}
	if !strings.Contains(w.Body.String(), `"upstream":true`) {
		t.Fatalf("上游响应体未回传: %s", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("响应 Content-Type 未回写: %s", ct)
	}

	// 非白名单域 → 403 JSON，且未触达上游
	gotMethod = ""
	evilTarget := "/" + tok + "/__wt__/upstream?" + url.Values{"url": {"http://bitwarden.eu/echo"}}.Encode()
	w2 := doUpstreamReq(svc, "POST", evilTarget, strings.NewReader(body), nil)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("非白名单域应 403, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "upstream host not allowed") {
		t.Fatalf("403 响应体不对: %s", w2.Body.String())
	}
	if gotMethod != "" {
		t.Fatal("被拒请求不应触达上游")
	}

	// HTML 注入：upstream 变量在桥脚本之前
	w3 := doGet(svc, "/"+tok+"/index.html")
	html := w3.Body.String()
	if !strings.Contains(html, "__WT_UPSTREAM_HOSTS__") {
		t.Fatalf("未注入 __WT_UPSTREAM_HOSTS__: %s", html)
	}
	if !strings.Contains(html, `window.__WT_UPSTREAM_HOSTS__=["*.bitwarden.com","127.0.0.1"]`) {
		t.Fatalf("注入内容不对: %s", html)
	}
	upIdx := strings.Index(html, "__WT_UPSTREAM_HOSTS__")
	bridgeIdx := strings.Index(html, `/host/bridge.js`)
	if upIdx == -1 || bridgeIdx == -1 || upIdx > bridgeIdx {
		t.Fatalf("upstream 变量应先于桥脚本注入: up=%d bridge=%d", upIdx, bridgeIdx)
	}
}

func TestNoUpstreamInjectWithoutHosts(t *testing.T) {
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"plain","unihub":{"id":"plain"}}`,
		"index.html":   `<html><head><title>p</title></head></html>`,
	})
	tok := r.TokenFor("plain")
	w := doGet(svc, "/"+tok+"/index.html")
	if strings.Contains(w.Body.String(), "__WT_UPSTREAM_HOSTS__") {
		t.Fatalf("无 upstreamHosts 不应注入: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `<script src="/host/bridge.js"></script>`) {
		t.Fatal("桥脚本注入被破坏")
	}
}

func TestInjectBridgeVersion(t *testing.T) {
	html := []byte(`<html><head><title>x</title></head><body></body></html>`)
	// 无版本：退回无查询标签
	if out := string(injectBridge(html, nil, "")); !strings.Contains(out, `<script src="/host/bridge.js"></script>`) {
		t.Fatalf("空版本应使用无查询标签: %s", out)
	}
	// 带版本：标签含 ?v=，upstream 变量仍在桥脚本之前
	out := string(injectBridge(html, []string{"*.bitwarden.com"}, "abcd1234"))
	if !strings.Contains(out, `<script src="/host/bridge.js?v=abcd1234"></script>`) {
		t.Fatalf("版本未进标签: %s", out)
	}
	if up, br := strings.Index(out, "__WT_UPSTREAM_HOSTS__"), strings.Index(out, "/host/bridge.js"); up == -1 || br == -1 || up > br {
		t.Fatalf("upstream 变量应先于桥脚本: up=%d bridge=%d", up, br)
	}
}
