// Package hostservice 暴露给宿主 UI（主框架）调用的 Wails 绑定服务。
// 注意：这些方法不接收来自插件 iframe 的调用——插件能力走
// pluginapi（token 网关 + 权限校验）。
package hostservice

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"webtool/internal/intercept"
	"webtool/internal/manifest"
	"webtool/internal/marketplace"
	"webtool/internal/plugin"
	"webtool/internal/router"
	"webtool/internal/server"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type PluginService struct {
	mgr      *plugin.Manager
	rtr      *router.Router
	assets   *server.AssetService
	rulesPath string
}

func NewPluginService(mgr *plugin.Manager, rtr *router.Router, assets *server.AssetService, rulesPath string) *PluginService {
	return &PluginService{mgr: mgr, rtr: rtr, assets: assets, rulesPath: rulesPath}
}

func (s *PluginService) ServiceName() string { return "webtool.host" }

// ListPlugins 返回全部已安装插件。
func (s *PluginService) ListPlugins() []plugin.Info {
	return s.mgr.List()
}

// InstallFromFile 弹原生文件选择器安装本地 zip 插件。
func (s *PluginService) InstallFromFile() (*plugin.Info, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil // 用户取消
	}
	if !strings.HasSuffix(strings.ToLower(path), ".zip") {
		return nil, fmt.Errorf("请选择 .zip 插件包")
	}
	return s.mgr.InstallZipPath(path, "file", "")
}

// InstallFromURL 从 URL 下载安装插件包。
func (s *PluginService) InstallFromURL(url string) (*plugin.Info, error) {
	if !(strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) {
		return nil, fmt.Errorf("URL 须以 http:// 或 https:// 开头")
	}
	return s.mgr.InstallURL(url, "url")
}

// UninstallPlugin 卸载插件（deleteData 同时删除其持久化数据）。
func (s *PluginService) UninstallPlugin(id string, deleteData bool) error {
	if err := s.mgr.Uninstall(id, deleteData); err != nil {
		return err
	}
	s.rtr.Revoke(id)
	return nil
}

// SetPluginEnabled 启用/停用插件。
func (s *PluginService) SetPluginEnabled(id string, enabled bool) error {
	return s.mgr.SetEnabled(id, enabled)
}

// OpenPlugin 返回插件入口的 iframe src（/p/<token>/<entry>）。
func (s *PluginService) OpenPlugin(id string) (string, error) {
	mf, err := s.mgr.Manifest(id)
	if err != nil {
		return "", err
	}
	info, err := s.mgr.Get(id)
	if err != nil {
		return "", err
	}
	if !info.Enabled {
		return "", fmt.Errorf("插件已停用")
	}
	token := s.rtr.TokenFor(id)
	if mf.Type == manifest.Bundle {
		return "/p/" + token + "/" + mf.Entry, nil
	}
	// web/link：入口即命名空间根
	return "/p/" + token + "/", nil
}

// CreateWebPlugin 捕获网页为 web 插件（默认录制模式：边用边录）。
func (s *PluginService) CreateWebPlugin(name, pageURL string) (*plugin.Info, error) {
	u, err := url.Parse(pageURL)
	if err != nil || !(u.Scheme == "http" || u.Scheme == "https") || u.Host == "" {
		return nil, fmt.Errorf("URL 须为合法的 http/https 地址")
	}
	if name == "" {
		name = strings.TrimSuffix(filepath_base(u.Path), "/")
		if name == "" {
			name = u.Host
		}
	}
	base := manifest.SanitizeID(u.Host)
	id := base
	for i := 2; ; i++ {
		if _, err := s.mgr.Get(id); err != nil {
			break // 未占用
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
	mf := &manifest.Manifest{
		ID:        id,
		Name:      name,
		Type:      manifest.Web,
		SourceURL: pageURL,
		Mode:      "record",
		Category:  "web",
		Icon:      "🌐",
	}
	info, err := s.mgr.CreateFromManifest(mf, "capture", pageURL)
	if err != nil {
		return nil, err
	}
	if s.assets != nil {
		s.assets.InvalidateMirrors(id)
	}
	return info, nil
}

// SetPluginMode 切换 web 插件模式（live 浏览 / record 录制 / offline 固化离线）。
func (s *PluginService) SetPluginMode(id, mode string) error {
	if err := s.mgr.UpdateWebMode(id, mode); err != nil {
		return err
	}
	if s.assets != nil {
		s.assets.InvalidateMirrors(id)
	}
	return nil
}

// GetGlobalRules 返回全局拦截规则。
func (s *PluginService) GetGlobalRules() []intercept.Rule {
	return server.LoadOrCreateGlobalRules(s.rulesPath)
}// SaveGlobalRules 校验并保存全局拦截规则（热生效）。
func (s *PluginService) SaveGlobalRules(rules []intercept.Rule) error {
	compiled, err := intercept.Compile(rules)
	if err != nil {
		return fmt.Errorf("规则校验失败: %w", err)
	}
	if err := server.SaveGlobalRules(s.rulesPath, rules); err != nil {
		return err
	}
	if s.assets != nil {
		s.assets.SetGlobalRules(compiled)
	}
	return nil
}

func filepath_base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ---- 插件市场（默认 unihub 官方源）----

// ListMarketplace 拉取市场目录（默认源为 unihub 官方）。
func (s *PluginService) ListMarketplace() ([]marketplace.Entry, error) {
	return marketplace.Fetch(marketplace.DefaultSource)
}

// InstallMarketplacePlugin 从市场条目安装（zip 下载 → 常规安装流程）。
func (s *PluginService) InstallMarketplacePlugin(zipURL, entryID string) (*plugin.Info, error) {
	if !(strings.HasPrefix(zipURL, "http://") || strings.HasPrefix(zipURL, "https://")) {
		return nil, fmt.Errorf("安装地址不合法")
	}
	return s.mgr.InstallURL(zipURL, "market:"+entryID)
}

// ReportPluginError 接收插件 iframe 的运行时错误（console/rejection），
// 落盘到 <dataDir>/logs/plugin-errors.log，便于排查 WebKitGTK 环境问题。
func (s *PluginService) ReportPluginError(pluginID, message string) {
	logDir := filepath.Join(filepath.Dir(s.rulesPath), "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(logDir, "plugin-errors.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := strings.ReplaceAll(message, "\n", " ⏎ ")
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	fmt.Fprintf(f, "[%s] %s: %s\n", time.Now().Format("2006-01-02 15:04:05"), pluginID, msg)
}

// PluginAssetURL 返回插件目录内相对资源的 URL（供图标等使用）。
func (s *PluginService) PluginAssetURL(id, relPath string) (string, error) {
	if _, err := s.mgr.Get(id); err != nil {
		return "", err
	}
	token := s.rtr.TokenFor(id)
	clean := strings.TrimPrefix(relPath, "/")
	if clean == "" {
		return "", fmt.Errorf("路径为空")
	}
	return "/p/" + token + "/" + clean, nil
}
