package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtool/internal/intercept"
)

// TestRefererRewriteMiddleware: 插件 iframe 内的绝对路径请求
// 应按 Referer 回填到来源插件的 token 空间（ctool 的 /chrome_tool/* 实测场景）。
func TestRefererRewriteMiddleware(t *testing.T) {
	var gotPath string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	})
	h := RefererRewriteMiddleware(next)

	// 来自插件 iframe 的绝对路径 → 重写
	req := httptest.NewRequest("GET", "/chrome_tool/notice?v=1", nil)
	req.Header.Set("Referer", "wails://localhost/p/abcdef0123456789abcdef0123456789/tool/index.html")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotPath != "/p/abcdef0123456789abcdef0123456789/chrome_tool/notice" {
		t.Fatalf("未重写: %s", gotPath)
	}

	// http referer 同样生效（harness 场景）
	req2 := httptest.NewRequest("GET", "/custom.js", nil)
	req2.Header.Set("Referer", "http://127.0.0.1:8899/p/abcdef0123456789abcdef0123456789/index.html")
	h.ServeHTTP(httptest.NewRecorder(), req2)
	if !strings.HasPrefix(gotPath, "/p/abcdef0123456789abcdef0123456789/custom.js") {
		t.Fatalf("http referer 未重写: %s", gotPath)
	}

	// 无插件 Referer 的普通请求不动
	req3 := httptest.NewRequest("GET", "/assets/index-abc.js", nil)
	req3.Header.Set("Referer", "wails://localhost/")
	h.ServeHTTP(httptest.NewRecorder(), req3)
	if gotPath != "/assets/index-abc.js" {
		t.Fatalf("宿主请求被误改: %s", gotPath)
	}

	// /p/ 与 /wails/ 前缀不动
	for _, p := range []string{"/p/tok1234567890abcdefgh/index.html", "/wails/runtime"} {
		req4 := httptest.NewRequest("GET", p, nil)
		req4.Header.Set("Referer", "wails://localhost/p/abcdef0123456789abcdef0123456789/x.html")
		h.ServeHTTP(httptest.NewRecorder(), req4)
		if gotPath != p {
			t.Fatalf("%s 被误改: %s", p, gotPath)
		}
	}
}

// TestGlobalRulesLegacyCtool: 全局规则文件（rules.json）承载旧版
// chrome_tool mock —— 不再硬编码，且 legacy zip（无 manifest）也生效。
func TestGlobalRulesLegacyCtool(t *testing.T) {
	m, svc, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"ctool-l","unihub":{"id":"ctool-l"}}`,
		"index.html":   "<html></html>",
	})
	svc.SetGlobalRules(DefaultGlobalRules())
	tok := r.TokenFor("ctool-l")

	// notice/stat 被 mock
	for _, p := range []string{"notice", "stat"} {
		w := doGet(svc, "/"+tok+"/chrome_tool/"+p+"?i=zh_CN&v=1.11.1")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "操作成功") {
			t.Fatalf("chrome_tool/%s 未被全局规则 mock: %d %s", p, w.Code, w.Body.String())
		}
	}

	// armconverter 等功能路径不受影响
	w2 := doGet(svc, "/"+tok+"/chrome_tool/armconverter/data.js")
	if w2.Code == 200 && strings.Contains(w2.Body.String(), "操作成功") {
		t.Fatal("armconverter 不应被 mock")
	}

	// 全局规则文件加载/落盘
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.json")
	rules := LoadOrCreateGlobalRules(rulesFile)
	if len(rules) == 0 {
		t.Fatal("默认全局规则为空")
	}
	if _, err := os.ReadFile(rulesFile); err != nil {
		t.Fatalf("规则文件未落盘: %v", err)
	}
	// 二次加载读文件
	rules2 := LoadOrCreateGlobalRules(rulesFile)
	if len(rules2) != len(rules) {
		t.Fatalf("二次加载数量不一致: %d vs %d", len(rules2), len(rules))
	}
	// 自定义规则生效
	custom, err := intercept.Compile([]intercept.Rule{{
		Match:  intercept.Match{PathRegex: `^/?blocked/`},
		Action: intercept.Action{Type: intercept.ActionBlock, Status: 410},
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetGlobalRules(custom)
	w3 := doGet(svc, "/"+tok+"/blocked/x")
	if w3.Code != 410 {
		t.Fatalf("自定义全局规则未生效: %d", w3.Code)
	}
}
