// Package intercept 实现数据驱动的请求拦截规则引擎。
// 规则来源于插件清单与全局设置，作用于插件资源服务器上的每个请求，
// 取代旧版 webtool 中硬编码的 chrome_tool 拦截。
package intercept

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// 拦截动作类型。
const (
	ActionPass       = "pass"        // 放行（bundle: 正常读文件；web/link: 回源）
	ActionBlock      = "block"       // 拦截，返回空响应
	ActionRespond    = "respond"     // 直接返回配置的 body（mock）
	ActionRewrite    = "rewrite"     // 重写路径后继续处理
	ActionCacheFirst = "cache-first" // （web 插件）强制走本地缓存
	ActionNoCache    = "no-cache"    // 禁用缓存
)

// Match 描述规则匹配条件。字段留空表示任意；*Regex 优先于精确匹配。
type Match struct {
	Host      string `json:"host,omitempty"`
	HostRegex string `json:"hostRegex,omitempty"`
	Path      string `json:"path,omitempty"`
	PathRegex string `json:"pathRegex,omitempty"`
}

// Action 描述命中后执行的动作。
type Action struct {
	Type        string `json:"type"`
	Status      int    `json:"status,omitempty"`      // block/respond 的 HTTP 状态码
	Body        string `json:"body,omitempty"`        // respond 的响应体
	ContentType string `json:"contentType,omitempty"` // respond 的 Content-Type
	To          string `json:"to,omitempty"`          // rewrite 目标路径，支持 $1..$9
}

type Rule struct {
	Name   string `json:"name,omitempty"`
	Match  Match  `json:"match"`
	Action Action `json:"action"`

	hostRe *regexp.Regexp
	pathRe *regexp.Regexp
}

// Compile 校验并预编译规则集里的正则，返回可直接用于 Evaluate 的规则。
func Compile(rules []Rule) ([]Rule, error) {
	out := make([]Rule, 0, len(rules))
	for i, r := range rules {
		switch r.Action.Type {
		case ActionPass, ActionBlock, ActionRespond, ActionRewrite, ActionCacheFirst, ActionNoCache:
		case "":
			return nil, fmt.Errorf("规则 #%d 缺少 action.type", i)
		default:
			return nil, fmt.Errorf("规则 #%d 未知 action.type: %q", i, r.Action.Type)
		}
		if r.Match.HostRegex != "" {
			re, err := regexp.Compile(r.Match.HostRegex)
			if err != nil {
				return nil, fmt.Errorf("规则 #%d hostRegex 无效: %w", i, err)
			}
			r.hostRe = re
		}
		if r.Match.PathRegex != "" {
			re, err := regexp.Compile(r.Match.PathRegex)
			if err != nil {
				return nil, fmt.Errorf("规则 #%d pathRegex 无效: %w", i, err)
			}
			r.pathRe = re
		}
		out = append(out, r)
	}
	return out, nil
}

func matchString(exact, regex string, re *regexp.Regexp, value string) bool {
	if regex != "" {
		return re != nil && re.MatchString(value)
	}
	if exact != "" {
		return strings.Contains(value, exact)
	}
	return true
}

// Evaluate 返回第一条命中的规则动作；无命中返回 nil。
// 兼容带/不带前导斜杠的 pathRegex/path 写法。
func Evaluate(rules []Rule, host, path string) *Rule {
	trimmed := strings.TrimPrefix(path, "/")
	for i := range rules {
		r := &rules[i]
		if !matchString(r.Match.Host, r.Match.HostRegex, r.hostRe, host) {
			continue
		}
		if matchString(r.Match.Path, r.Match.PathRegex, r.pathRe, path) ||
			(trimmed != path && matchString(r.Match.Path, r.Match.PathRegex, r.pathRe, trimmed)) {
			return r
		}
	}
	return nil
}

// RewritePath 按 rewrite 目标重写路径，$1..$9 引用 pathRegex 捕获组。
// 无捕获组时直接返回 To。
func RewritePath(r *Rule, path string) string {
	if r.pathRe == nil {
		return r.Action.To
	}
	m := r.pathRe.FindStringSubmatch(path)
	if m == nil {
		return r.Action.To
	}
	out := r.Action.To
	for i := 1; i < len(m) && i <= 9; i++ {
		out = strings.ReplaceAll(out, fmt.Sprintf("$%d", i), m[i])
	}
	return out
}

// Apply 将动作写到 http.ResponseWriter 上（block/respond），
// 返回 true 表示响应已写完、请求处理终止。
func Apply(w http.ResponseWriter, a Action) bool {
	switch a.Type {
	case ActionBlock:
		status := a.Status
		if status == 0 {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		return true
	case ActionRespond:
		status := a.Status
		if status == 0 {
			status = http.StatusOK
		}
		ct := a.ContentType
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(a.Body))
		return true
	}
	return false
}
