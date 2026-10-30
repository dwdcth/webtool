package intercept

import (
	"net/http/httptest"
	"testing"
)

func mustCompile(t *testing.T, rules []Rule) []Rule {
	t.Helper()
	out, err := Compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEvaluateRegexRespond(t *testing.T) {
	rules := mustCompile(t, []Rule{{
		Name: "ctool-stat-mock",
		Match: Match{
			PathRegex: `^chrome_tool/(notice|stat)`,
		},
		Action: Action{Type: ActionRespond, Status: 200, Body: `{"code":0}`, ContentType: "application/json"},
	}})
	// 旧版 webtool 硬编码行为的等价规则
	for _, path := range []string{"/chrome_tool/notice", "/chrome_tool/stat"} {
		r := Evaluate(rules, "localhost", path)
		if r == nil || r.Action.Type != ActionRespond {
			t.Fatalf("%s 应命中 mock 规则", path)
		}
	}
	if Evaluate(rules, "localhost", "/chrome_tool/armconverter/x") != nil {
		t.Fatal("armconverter 不应被拦截（旧版保留该路径）")
	}
	if Evaluate(rules, "localhost", "/normal.js") != nil {
		t.Fatal("无关路径不应命中")
	}
}

func TestEvaluateHostAndExact(t *testing.T) {
	rules := mustCompile(t, []Rule{
		{Name: "block-ads", Match: Match{Host: "ads.example.com"}, Action: Action{Type: ActionBlock}},
		{Name: "exact", Match: Match{Path: "/exact/file.js"}, Action: Action{Type: ActionRespond}},
	})
	if r := Evaluate(rules, "ads.example.com", "/x"); r == nil || r.Action.Type != ActionBlock {
		t.Fatal("host 匹配失败")
	}
	if Evaluate(rules, "good.example.com", "/x") != nil {
		t.Fatal("host 不匹配应放行")
	}
	if r := Evaluate(rules, "any", "/exact/file.js"); r == nil {
		t.Fatal("精确路径匹配失败")
	}
}

func TestRewrite(t *testing.T) {
	rules := mustCompile(t, []Rule{{
		Match:  Match{PathRegex: `^/old/(.+)$`},
		Action: Action{Type: ActionRewrite, To: "/new/$1"},
	}})
	r := Evaluate(rules, "h", "/old/app.js")
	if r == nil {
		t.Fatal("未命中")
	}
	if got := RewritePath(r, "/old/app.js"); got != "/new/app.js" {
		t.Fatalf("rewrite 结果错误: %q", got)
	}
}

func TestApplyRespondAndBlock(t *testing.T) {
	w := httptest.NewRecorder()
	done := Apply(w, Action{Type: ActionRespond, Status: 200, Body: "hi", ContentType: "text/plain"})
	if !done || w.Code != 200 || w.Body.String() != "hi" {
		t.Fatalf("respond 应用失败: code=%d body=%q", w.Code, w.Body.String())
	}
	w2 := httptest.NewRecorder()
	done = Apply(w2, Action{Type: ActionBlock})
	if !done || w2.Code != 404 {
		t.Fatalf("block 应用失败: %d", w2.Code)
	}
	w3 := httptest.NewRecorder()
	if Apply(w3, Action{Type: ActionPass}) {
		t.Fatal("pass 不应终结请求")
	}
}

func TestCompileRejectsBadRegex(t *testing.T) {
	if _, err := Compile([]Rule{{Match: Match{PathRegex: "("}, Action: Action{Type: ActionBlock}}}); err == nil {
		t.Fatal("非法正则应报错")
	}
	if _, err := Compile([]Rule{{Action: Action{Type: "bogus"}}}); err == nil {
		t.Fatal("未知动作应报错")
	}
}
