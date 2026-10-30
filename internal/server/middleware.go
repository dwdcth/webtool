package server

import (
	"net/http"
	"regexp"
	"strings"
)

// refererTokenRe 从路径里提取插件 token 前缀（/p/<token>/…）。
var refererTokenRe = regexp.MustCompile(`^/p/([0-9a-f]{16,64})/`)

// RefererRewriteMiddleware 处理插件页面内的绝对路径请求。
//
// 现象（ctool 实测）：插件 HTML/JS 用绝对路径请求 /chrome_tool/notice、
// /custom.js 等，这些请求绕过 /p/<token>/ 命名空间直接打到应用根，
// 得到 404。旧版 webtool 靠在根上硬编码 chrome_tool 拦截解决。
//
// 通用做法：非 /p/、/wails/、/host/ 的请求若带 /p/<token>/ 的 Referer
// （即来自某个插件 iframe），把路径重写回该插件的 token 空间，让它走
// 正常的插件资产服务（并应用插件规则与全局规则）。
func RefererRewriteMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/p/") &&
			!strings.HasPrefix(p, "/wails/") &&
			!strings.HasPrefix(p, "/host/") {
			if m := refererTokenRe.FindStringSubmatch(referrerPath(r)); m != nil {
				r.URL.Path = "/p/" + m[1] + p
			}
		}
		next.ServeHTTP(w, r)
	})
}

// referrerPath 从 Referer URL 中取路径部分（无 Referer 返回空串）。
func referrerPath(r *http.Request) string {
	ref := r.Referer()
	if ref == "" {
		return ""
	}
	// 形如 wails://localhost/p/<token>/x 或 http://host/p/<token>/x
	if i := strings.Index(ref, "://"); i >= 0 {
		rest := ref[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[j:]
		}
		return "/"
	}
	return ref
}
