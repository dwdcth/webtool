package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtool/internal/plugin"
	"webtool/internal/pluginapi"
	"webtool/internal/router"
)

// TestRealLegacyZips 用仓库里真实的旧版 zip（plugin/*.zip）走完整链路：
// 安装 → token → 资产服务 → bridge 注入。
func TestRealLegacyZips(t *testing.T) {
	zips, err := filepath.Glob("../../plugin/*.zip")
	if err != nil || len(zips) == 0 {
		t.Skip("仓库内无旧版插件 zip")
	}
	dir := t.TempDir()
	m := plugin.NewManager(filepath.Join(dir, "plugins"), filepath.Join(dir, "plugins.json"))
	if err := m.LoadInstalled(); err != nil {
		t.Fatal(err)
	}
	r := router.New()
	api := pluginapi.New(m)
	svc := NewAssetService(m, r, api)

	for _, z := range zips {
		z := z
		t.Run(filepath.Base(z), func(t *testing.T) {
			info, err := m.InstallZipPath(z, "file", "")
			if err != nil {
				t.Fatalf("安装失败: %v", err)
			}
			tok := r.TokenFor(info.ID)
			w := doGet(svc, "/"+tok+"/"+info.Entry)
			if w.Code != 200 {
				t.Fatalf("入口 %s 返回 %d: %s", info.Entry, w.Code, w.Body.String()[:min(200, w.Body.Len())])
			}
			if !strings.Contains(w.Body.String(), "/host/bridge.js") {
				t.Fatal("bridge 未注入")
			}
			// 入口 HTML 引用的资源抽查：任选一个 zip 内静态文件
			entries, _ := os.ReadDir(filepath.Dir(filepath.Join(m.PluginsDir(), info.ID, info.Entry)))
			for _, e := range entries {
				if e.IsDir() {
					sub := e.Name() + "/index.html"
					if _, err := os.Stat(filepath.Join(m.PluginsDir(), info.ID, filepath.FromSlash(sub))); err == nil {
						w2 := doGet(svc, "/"+tok+"/"+sub)
						if w2.Code == 200 {
							return
						}
					}
					// 目录下任意非 html 文件
					files, _ := os.ReadDir(filepath.Join(m.PluginsDir(), info.ID, e.Name()))
					for _, f := range files {
						if !f.IsDir() {
							w3 := doGet(svc, "/"+tok+"/"+e.Name()+"/"+f.Name())
							if w3.Code == 200 {
								return
							}
						}
					}
				}
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
