package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseUnihubCompat(t *testing.T) {
	raw := []byte(`{
		"name": "simple-calculator",
		"version": "1.0.0",
		"description": "计算器",
		"author": "alice",
		"keywords": ["calc"],
		"homepage": "https://example.com",
		"unihub": {
			"id": "com.unihub.simple-calculator",
			"name": "简单计算器",
			"icon": "🧮",
			"category": "tool",
			"entry": "dist/index.html",
			"permissions": ["clipboard", "storage"]
		}
	}`)
	m, err := Parse(raw, "fallback", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	// unihub 字段优先
	if m.ID != "com.unihub.simple-calculator" || m.Name != "简单计算器" || m.Entry != "dist/index.html" {
		t.Fatalf("unihub 字段未生效: %+v", m)
	}
	// npm 字段继承
	if m.Version != "1.0.0" || m.Author != "alice" || m.Homepage != "https://example.com" {
		t.Fatalf("npm 字段继承失败: %+v", m)
	}
	if len(m.Permissions) != 2 {
		t.Fatalf("permissions 解析失败: %v", m.Permissions)
	}
	if m.Type != Bundle {
		t.Fatalf("缺省类型应为 bundle: %v", m.Type)
	}
}

func TestParseInheritsIDFromName(t *testing.T) {
	// 无 unihub 节的普通 package.json：id 从 name 继承
	m, err := Parse([]byte(`{"name":"my-plugin","version":"2.0.0"}`), "fb", "fb")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "my-plugin" || m.Version != "2.0.0" {
		t.Fatalf("继承失败: %+v", m)
	}
}

func TestParseLegacyZipWithoutManifest(t *testing.T) {
	// 旧版 webtool 的 zip：无 package.json，fallback 合成
	m, err := Parse(nil, "ctool", "ctool")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "ctool" || m.Name != "ctool" || m.Type != Bundle {
		t.Fatalf("fallback 清单错误: %+v", m)
	}
}

func TestParseWebtoolExtension(t *testing.T) {
	raw := []byte(`{
		"name": "ctool",
		"unihub": {"id": "ctool"},
		"webtool": {
			"type": "web",
			"sourceUrl": "https://ctool.aizhan.com/",
			"interceptors": [
				{"match": {"pathRegex": "^chrome_tool/(notice|stat)"},
				 "action": {"type": "respond", "status": 200,
				            "body": "{\"code\":0}", "contentType": "application/json"}}
			]
		}
	}`)
	m, err := Parse(raw, "fb", "fb")
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != Web || m.SourceURL != "https://ctool.aizhan.com/" {
		t.Fatalf("webtool 扩展解析失败: %+v", m)
	}
	if len(m.Interceptors) != 1 {
		t.Fatalf("interceptors 解析失败: %v", m.Interceptors)
	}
}

func TestParseRejectsBadID(t *testing.T) {
	if _, err := Parse(nil, "../etc", "x"); err == nil {
		t.Fatal("路径注入 id 应被拒绝")
	}
	if _, err := Parse(nil, "a/b", "x"); err == nil {
		t.Fatal("含分隔符 id 应被拒绝")
	}
}

func TestInferEntryPriority(t *testing.T) {
	dir := t.TempDir()
	// 根目录 index.html
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644)
	m := &Manifest{}
	InferEntry(dir, m)
	if m.Entry != "index.html" {
		t.Fatalf("应选根 index.html, got %q", m.Entry)
	}
	// dist/ 优先于根
	os.MkdirAll(filepath.Join(dir, "dist"), 0o755)
	os.WriteFile(filepath.Join(dir, "dist", "index.html"), []byte("x"), 0o644)
	m2 := &Manifest{}
	InferEntry(dir, m2)
	if m2.Entry != "dist/index.html" {
		t.Fatalf("dist 应优先, got %q", m2.Entry)
	}
	// 显式 Entry 不被覆盖
	m3 := &Manifest{Entry: "custom/index.html"}
	InferEntry(dir, m3)
	if m3.Entry != "custom/index.html" {
		t.Fatalf("显式 entry 不应被覆盖: %q", m3.Entry)
	}
}

func TestSanitizeID(t *testing.T) {
	if got := SanitizeID("CTool 2"); got != "ctool-2" {
		t.Fatalf("got %q", got)
	}
	// 所有产物必须合法（含中文等非 ASCII 输入的 crc32 兜底）
	for _, in := range []string{"CTool 2", "../evil", "a/b\\c", "正常中文", "", "---"} {
		got := SanitizeID(in)
		if !ValidID(got) {
			t.Fatalf("SanitizeID(%q) 产物 %q 不合法", in, got)
		}
	}
}
