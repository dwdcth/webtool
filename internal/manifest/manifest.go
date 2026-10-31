// Package manifest 解析插件清单。
// 兼容 unihub 的 package.json 格式（unihub 字段 + 标准 npm 字段继承），
// 并扩展 webtool 字段（type / sourceUrl / interceptors）。
// 无清单的 legacy zip（如旧版 webtool 的 ctool.zip）也能安装：
// 以 zip 文件名合成 id，自动推断 entry。
package manifest

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"webtool/internal/intercept"
)

type PluginType string

const (
	// Bundle 打包插件：解压目录直接服务（unihub 语义）。
	Bundle PluginType = "bundle"
	// Web 网页镜像插件：本地镜像缓存 + 回源代理（P2）。
	Web PluginType = "web"
	// Link 在线直连插件：不缓存（P2）。
	Link PluginType = "link"
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,99}$`)

// ValidID 校验插件 id（同时也是安装目录名，必须防止路径注入）。
func ValidID(id string) bool {
	return idPattern.MatchString(id) && !strings.ContainsAny(id, `/\`)
}

// SanitizeID 把任意名字（如 zip 文件名）转成合法 id。
// 非 ASCII 名字（如中文）清洗后为空时，用 crc32 十六进制兜底。
func SanitizeID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-.")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = fmt.Sprintf("p-%08x", crc32.ChecksumIEEE([]byte(name)))
	}
	return s
}

// Manifest 是解析合并后的插件元数据。
type Manifest struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description"`
	Author       string            `json:"author"`
	Icon         string            `json:"icon"` // emoji / URL / 相对路径
	Category     string            `json:"category"`
	Keywords     []string          `json:"keywords"`
	Entry        string            `json:"entry"` // 相对插件目录的入口 HTML
	Permissions  []string          `json:"permissions"`
	Homepage     string            `json:"homepage"`
	Type         PluginType        `json:"type"`
	SourceURL    string            `json:"sourceUrl,omitempty"` // web/link 插件的远程地址
	Mode         string            `json:"mode,omitempty"`      // web 插件：live|record|offline
	DevURL       string            `json:"devUrl,omitempty"`    // 开发模式：直载 dev server
	Sandbox       bool              `json:"sandbox,omitempty"` // 严格沙箱（默认关闭，兼容 WASM/Worker 插件）
	Interceptors  []intercept.Rule  `json:"interceptors,omitempty"`
	UpstreamHosts []string          `json:"upstreamHosts,omitempty"` // 允许经 /p/<token>/__wt__/upstream 代理的上游域名
	Dir           string            `json:"-"`                       // 安装目录（运行时填充）
}

// package.json 原始结构（unihub 兼容）。
type pkgJSON struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      author   `json:"author"`
	Keywords    []string `json:"keywords"`
	Homepage    string   `json:"homepage"`
	UniHub      *uniHub  `json:"unihub"`
	Webtool     *webtool `json:"webtool"`
}

type uniHub struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      author   `json:"author"`
	Icon        string   `json:"icon"`
	Category    string   `json:"category"`
	Keywords    []string `json:"keywords"`
	Entry       string   `json:"entry"`
	Permissions []string `json:"permissions"`
	Homepage    string   `json:"homepage"`
	DevURL      string   `json:"devUrl"`
}

type webtool struct {
	Type         PluginType       `json:"type"`
	SourceURL    string           `json:"sourceUrl"`
	Mode         string           `json:"mode"`   // web 插件：live | record | offline
	DevURL       string           `json:"devUrl"` // 开发模式：直载 vite dev server（http://localhost:5173）
	Interceptors []intercept.Rule `json:"interceptors"`
	// Sandbox 开启严格沙箱 iframe（无 allow-same-origin）。
	// 默认关闭：WASM/Worker 类插件（如 pearocr）在 opaque origin 下
	// 无法创建 Worker、fetch 需跨域授权，兼容性优先。
	Sandbox bool `json:"sandbox"`
	// UpstreamHosts 声明本插件允许代理转发的上游域名，
	// 支持 *.example.com 通配或精确域名。
	UpstreamHosts []string `json:"upstreamHosts"`
}

// author 兼容 "string" 与 {name: "..."} 两种写法。
type author struct {
	Value string
}

func (a *author) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	if strings.HasPrefix(s, "{") {
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(b, &obj); err != nil {
			return err
		}
		a.Value = obj.Name
		return nil
	}
	return json.Unmarshal(b, &a.Value)
}

// Parse 解析 package.json 内容并合并 unihub 字段继承规则。
// raw 为空时（legacy zip 无清单），用 fallback 合成最小清单。
func Parse(raw []byte, fallbackID, fallbackName string) (*Manifest, error) {
	m := &Manifest{
		ID:       fallbackID,
		Name:     fallbackName,
		Version:  "0.0.0",
		Category: "tool",
		Type:     Bundle,
		Entry:    "",
	}

	if len(strings.TrimSpace(string(raw))) > 0 {
		var pkg pkgJSON
		if err := json.Unmarshal(raw, &pkg); err != nil {
			return nil, fmt.Errorf("package.json 解析失败: %w", err)
		}
		// 标准 npm 字段打底
		m.Name = orDefault(pkg.Name, m.Name)
		m.Version = orDefault(pkg.Version, m.Version)
		m.Description = pkg.Description
		m.Author = pkg.Author.Value
		m.Keywords = pkg.Keywords
		m.Homepage = pkg.Homepage

		// unihub 字段覆盖 + id 继承
		if pkg.UniHub != nil {
			u := pkg.UniHub
			m.ID = orDefault(u.ID, pkg.Name)
			m.Name = orDefault(u.Name, m.Name)
			m.Version = orDefault(u.Version, m.Version)
			m.Description = orDefault(u.Description, m.Description)
			if u.Author.Value != "" {
				m.Author = u.Author.Value
			}
			m.Icon = u.Icon
			m.Category = orDefault(u.Category, m.Category)
			if len(u.Keywords) > 0 {
				m.Keywords = u.Keywords
			}
			m.Entry = u.Entry
			m.Permissions = u.Permissions
			m.Homepage = orDefault(u.Homepage, m.Homepage)
		} else {
			// 无 unihub 节：从 npm 字段继承 id
			m.ID = orDefault(pkg.Name, m.ID)
		}

		// webtool 扩展
		if pkg.Webtool != nil {
			if pkg.Webtool.Type != "" {
				m.Type = pkg.Webtool.Type
			}
			m.SourceURL = pkg.Webtool.SourceURL
			m.Mode = pkg.Webtool.Mode
			m.DevURL = pkg.Webtool.DevURL
			m.Sandbox = pkg.Webtool.Sandbox
			m.UpstreamHosts = pkg.Webtool.UpstreamHosts
			if len(pkg.Webtool.Interceptors) > 0 {
				rules, err := intercept.Compile(pkg.Webtool.Interceptors)
				if err != nil {
					return nil, fmt.Errorf("interceptors 校验失败: %w", err)
				}
				m.Interceptors = rules
			}
		}
	}

	if !ValidID(m.ID) {
		return nil, fmt.Errorf("插件 id %q 不合法（需匹配 %s，且不能含路径分隔符）", m.ID, idPattern.String())
	}
	if m.Type != Bundle && m.Type != Web && m.Type != Link {
		return nil, fmt.Errorf("未知插件类型 %q（支持 bundle/web/link）", m.Type)
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	return m, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// InferEntry 在 Entry 为空时从插件目录推断入口 HTML。
// 优先级与 unihub 一致：dist/ > frontend/ > 根目录 > 任意子目录（深度 3）。
func InferEntry(dir string, m *Manifest) {
	if m.Entry != "" {
		return
	}
	for _, cand := range []string{
		filepath.Join("dist", "index.html"),
		filepath.Join("frontend", "index.html"),
		"index.html",
	} {
		if fileExists(filepath.Join(dir, filepath.FromSlash(cand))) {
			m.Entry = cand
			return
		}
	}
	// 递归找（深度 3，跳过常见无关目录）
	found := searchIndexHTML(dir, 3)
	if found != "" {
		rel, err := filepath.Rel(dir, found)
		if err == nil {
			m.Entry = filepath.ToSlash(rel)
		}
	}
}

func searchIndexHTML(dir string, depth int) string {
	if depth <= 0 {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "node_modules" || strings.HasPrefix(name, ".") {
			continue
		}
		cand := filepath.Join(dir, name, "index.html")
		if fileExists(cand) {
			return cand
		}
		if deeper := searchIndexHTML(filepath.Join(dir, name), depth-1); deeper != "" {
			return deeper
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
