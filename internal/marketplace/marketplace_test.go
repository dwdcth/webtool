package marketplace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleCatalog = `{
  "version": "1.0.0",
  "plugins": [
    {
      "id": "com.unihub.jwt-tool",
      "name": "JWT 工具",
      "version": "1.2.0",
      "description": "解码/编码/验证 JWT",
      "author": {"name": "UniHub Team", "url": "https://unihub.dev"},
      "icon": "https://api.iconify.design/twemoji/key.svg",
      "category": "tool",
      "permissions": ["clipboard"],
      "downloads": 1234,
      "install": {"zip": "https://cos.example.com/jwt/plugin.zip", "npm": "@unihub/jwt"}
    },
    {"id": "broken-no-zip", "name": "无安装地址", "install": {}},
    {
      "id": "com.unihub.simple",
      "name": "简单计算器",
      "author": "alice",
      "install": {"zip": "https://cos.example.com/calc/plugin.zip"}
    }
  ]
}`

func TestFetchParsesCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, sampleCatalog)
	}))
	defer srv.Close()

	entries, err := Fetch(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应过滤无 zip 条目, got %d", len(entries))
	}
	jwt := entries[0]
	if jwt.ID != "com.unihub.jwt-tool" || jwt.Name != "JWT 工具" || jwt.Version != "1.2.0" {
		t.Fatalf("字段解析错误: %+v", jwt)
	}
	if jwt.Author != "UniHub Team" {
		t.Fatalf("author 对象解析失败: %q", jwt.Author)
	}
	if jwt.ZipURL != "https://cos.example.com/jwt/plugin.zip" {
		t.Fatalf("zipUrl 解析失败: %q", jwt.ZipURL)
	}
	if jwt.Downloads != 1234 || len(jwt.Permissions) != 1 {
		t.Fatalf("附加字段解析失败: %+v", jwt)
	}
	// author 字符串形式
	if entries[1].Author != "alice" {
		t.Fatalf("author 字符串解析失败: %q", entries[1].Author)
	}
}

func TestFetchErrors(t *testing.T) {
	if _, err := Fetch("http://127.0.0.1:1/none.json"); err == nil {
		t.Fatal("不可达源应报错")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()
	if _, err := Fetch(srv.URL); err == nil || !strings.Contains(err.Error(), "解析失败") {
		t.Fatalf("坏 JSON 应报错: %v", err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv2.Close()
	if _, err := Fetch(srv2.URL); err == nil {
		t.Fatal("HTTP 500 应报错")
	}
}
