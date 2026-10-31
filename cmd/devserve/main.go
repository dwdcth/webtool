// devserve：本地 harness，把 AssetService 挂到普通 HTTP 端口，
// 用与真实应用一致的 sandbox iframe 承载插件，用于浏览器实测调试。
// 用法：go run -tags gtk3 ./cmd/devserve [端口]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"webtool/internal/paths"
	"webtool/internal/plugin"
	"webtool/internal/pluginapi"
	"webtool/internal/router"
	"webtool/internal/server"
)

const harnessHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>WebTool Harness</title>
<style>body{font:14px sans-serif;background:#111;color:#eee;padding:12px}
iframe{width:100%;height:85vh;border:1px solid #444;background:#fff}</style></head>
<body>
<div id="nav">%LINKS%</div>
<iframe id="frame"></iframe>
<!-- 与真实 App 的默认承载一致：无 sandbox（兼容 WASM/Worker/localStorage 插件）；
     真实 App 中 manifest 声明 webtool.sandbox=true 时才加沙箱 -->
<script>
window.__msgs = [];
window.addEventListener('message', e => {
  window.__msgs.push(JSON.stringify(e.data));
  console.log('[bridge-msg]', e.data);
});
console.log('[harness] ready');
function open(url){ document.getElementById('frame').src = url; console.log('[harness] open', url); }
</script>
</body></html>`

func main() {
	port := "8899"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}
	if err := paths.EnsureDirs(); err != nil {
		panic(err)
	}
	mgr := plugin.NewManager(paths.PluginsDir(), filepath.Join(paths.DataDir(), "plugins.json"))
	if err := mgr.LoadInstalled(); err != nil {
		panic(err)
	}
	rtr := router.New()
	api := pluginapi.New(mgr)
	svc := server.NewAssetService(mgr, rtr, api)
	// 桥脚本版本化（与真实应用一致，防 webview 旧缓存）
	if b, err := os.ReadFile("frontend/public/host/bridge.js"); err == nil {
		sum := sha256.Sum256(b)
		svc.SetBridgeVersion(hex.EncodeToString(sum[:4]))
	}
	svc.SetGlobalRules(server.LoadOrCreateGlobalRules(filepath.Join(paths.DataDir(), "rules.json")))

	mux := http.NewServeMux()
	mux.Handle("/p/", http.StripPrefix("/p", logRequests(svc)))
	// 与真实应用一致：/host/bridge.js 由资产服务注入到插件 HTML
	mux.Handle("/host/", http.StripPrefix("/host", http.FileServer(http.Dir("frontend/public/host"))))

	mux.HandleFunc("/harness.html", func(w http.ResponseWriter, r *http.Request) {
		var links []string
		for _, p := range mgr.List() {
			mf, _ := mgr.Manifest(p.ID)
			if mf == nil {
				continue
			}
			url := "/p/" + rtr.TokenFor(p.ID) + "/" + mf.Entry
			links = append(links, fmt.Sprintf(
				`<button onclick="open('%s')">%s</button> `, url, p.Name))
		}
		html := strings.Replace(harnessHTML, "%LINKS%", strings.Join(links, " "), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	})

	fmt.Printf("harness: http://127.0.0.1:%s/harness.html\n", port)
	if err := http.ListenAndServe("127.0.0.1:"+port, server.RefererRewriteMiddleware(mux)); err != nil {
		panic(err)
	}
}

// logRequests 打印每个插件请求的方法/路径/状态，便于排查。
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		fmt.Printf("%s %s -> %d\n", r.Method, r.URL.RequestURI(), rw.status)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
