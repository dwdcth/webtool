// Package plugin 管理插件生命周期：安装（zip/URL）、卸载、启停、列举。
package plugin

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"webtool/internal/manifest"
	"webtool/internal/registry"
)

// Info 是暴露给前端与 API 层的插件信息。
type Info struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Description     string             `json:"description"`
	Author          string             `json:"author"`
	Icon            string             `json:"icon"`
	Category        string             `json:"category"`
	Keywords        []string           `json:"keywords"`
	Permissions     []string           `json:"permissions"`
	Homepage        string             `json:"homepage"`
	Type            manifest.PluginType `json:"type"`
	SourceURL       string             `json:"sourceUrl,omitempty"`
	Mode            string             `json:"mode,omitempty"` // web 插件：live|record|offline
	Sandbox         bool               `json:"sandbox"`
	Enabled         bool               `json:"enabled"`
	InstalledAt     time.Time          `json:"installedAt"`
	Source          string             `json:"source"`
	Entry           string             `json:"entry"`
	HasInterceptors bool               `json:"hasInterceptors"`
}

type Manager struct {
	mu         sync.RWMutex
	pluginsDir string
	reg        *registry.Registry
	cache      map[string]*manifest.Manifest
}

func NewManager(pluginsDir string, registryPath string) *Manager {
	return &Manager{
		pluginsDir: pluginsDir,
		reg:        registry.Load(registryPath),
		cache:      map[string]*manifest.Manifest{},
	}
}

// PluginsDir 返回插件安装根目录。
func (m *Manager) PluginsDir() string { return m.pluginsDir }

// LoadInstalled 启动时校验注册表：清掉目录已不存在的记录。
func (m *Manager) LoadInstalled() error {
	for _, rec := range m.reg.List() {
		dir := m.pluginDir(rec.ID)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			_ = m.reg.Remove(rec.ID)
			continue
		}
		if _, err := m.Manifest(rec.ID); err != nil {
			// 清单损坏不卸载，仅标记；Manifest() 内部已缓存 nil
			fmt.Printf("[plugin] %s 清单加载失败: %v\n", rec.ID, err)
		}
	}
	return nil
}

func (m *Manager) pluginDir(id string) string {
	return filepath.Join(m.pluginsDir, id)
}

// InstallZipPath 从本地 zip 文件安装。
func (m *Manager) InstallZipPath(zipPath, source, sourceURL string) (*Info, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return nil, fmt.Errorf("打开插件包失败: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(filepath.Base(zipPath), ".zip")
	return m.installZip(f, st.Size(), base, source, sourceURL)
}

// InstallURL 下载并安装（带重试）。
func (m *Manager) InstallURL(url, source string) (*Info, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
		resp, err := http.Get(url + "?_t=" + fmt.Sprint(time.Now().UnixMilli())) //nolint:noctx // 下载由客户端触发
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
			continue
		}
		info, err := m.installZipReader(resp.Body, url, source, url)
		resp.Body.Close()
		if err == nil {
			return info, nil
		}
		lastErr = err
		if errors.Is(err, errBadPackage) {
			return nil, err // 包本身有问题，重试无意义
		}
	}
	return nil, fmt.Errorf("安装失败（已重试）: %w", lastErr)
}

var errBadPackage = errors.New("插件包无效")

// installZipReader 从流安装（URL 下载场景，先落盘再复用 installZip）。
func (m *Manager) installZipReader(r io.Reader, url, source, sourceURL string) (*Info, error) {
	tmp, err := os.CreateTemp("", "webtool-plugin-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	base := "plugin"
	if seg := strings.Split(strings.TrimRight(url, "/"), "/"); len(seg) > 0 && seg[len(seg)-1] != "" {
		base = strings.TrimSuffix(seg[len(seg)-1], ".zip")
	}
	f2, err := os.Open(tmp.Name())
	if err != nil {
		return nil, err
	}
	defer f2.Close()
	st2, err := f2.Stat()
	if err != nil {
		return nil, err
	}
	return m.installZip(f2, st2.Size(), base, source, sourceURL)
}

// installZip 的核心：解压到临时目录、读清单、校验、原子移动到插件目录。
func (m *Manager) installZip(f *os.File, size int64, fallbackName, source, sourceURL string) (*Info, error) {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return nil, fmt.Errorf("%w: 不是有效的 zip: %v", errBadPackage, err)
	}

	// 兼容 zip 根目录或单层顶层目录下的 package.json
	pkgRaw, err := readPackageJSON(zr)
	fallbackID := manifest.SanitizeID(fallbackName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	mf, err := manifest.Parse(pkgRaw, fallbackID, fallbackName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errBadPackage, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.pluginsDir, 0o755); err != nil {
		return nil, err
	}
	if _, exists := m.reg.Get(mf.ID); exists {
		return nil, fmt.Errorf("插件 %s 已安装", mf.ID)
	}

	tmpDir, err := os.MkdirTemp(m.pluginsDir, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	if err := extractZip(zr, tmpDir); err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}

	// manifest 可能位于单层顶层目录内，将其提升为插件根
	root := tmpDir
	if pkgRoot := locatePackageRoot(zr); pkgRoot != "" {
		root = filepath.Join(tmpDir, pkgRoot)
	}

	manifest.InferEntry(root, mf)
	if mf.Entry == "" {
		return nil, fmt.Errorf("%w: 找不到入口 index.html", errBadPackage)
	}
	mf.Dir = m.pluginDir(mf.ID)

	finalDir := m.pluginDir(mf.ID)
	if err := os.Rename(root, finalDir); err != nil {
		return nil, fmt.Errorf("落盘失败: %w", err)
	}

	// 远程图标本地化（iconify 等外链在部分网络不可达，落盘保证离线可见）
	m.localizeIcon(mf)

	rec := registry.Record{
		ID:          mf.ID,
		Version:     mf.Version,
		Enabled:     true,
		InstalledAt: time.Now(),
		Source:      source,
		SourceURL:   sourceURL,
	}
	if err := m.reg.Upsert(rec); err != nil {
		_ = os.RemoveAll(finalDir)
		return nil, err
	}
	m.cache[mf.ID] = mf
	info := m.toInfo(mf, rec)
	return &info, nil
}

// CreateFromManifest 以编程方式创建插件（网页捕获流程用）：
// 写 package.json 到插件目录并注册。要求目录不存在。
func (m *Manager) CreateFromManifest(mf *manifest.Manifest, source, sourceURL string) (*Info, error) {
	if !manifest.ValidID(mf.ID) {
		return nil, fmt.Errorf("插件 id %q 不合法", mf.ID)
	}
	if mf.Name == "" {
		mf.Name = mf.ID
	}
	if mf.Version == "" {
		mf.Version = "0.1.0"
	}
	if mf.Category == "" {
		mf.Category = "web"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.pluginsDir, 0o755); err != nil {
		return nil, err
	}
	if _, exists := m.reg.Get(mf.ID); exists {
		return nil, fmt.Errorf("插件 %s 已安装", mf.ID)
	}
	dir := m.pluginDir(mf.ID)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("插件目录已存在：%s", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// 序列化为 unihub 兼容 package.json
	pkg := map[string]any{
		"name":    mf.ID,
		"version": mf.Version,
		"unihub": map[string]any{
			"id":       mf.ID,
			"name":     mf.Name,
			"version":  mf.Version,
			"icon":     mf.Icon,
			"category": mf.Category,
		},
		"webtool": map[string]any{
			"type":      string(mf.Type),
			"sourceUrl": mf.SourceURL,
			"mode":      mf.Mode,
		},
	}
	b, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), b, 0o644); err != nil {
		return nil, err
	}

	mf.Dir = dir
	rec := registry.Record{
		ID:          mf.ID,
		Version:     mf.Version,
		Enabled:     true,
		InstalledAt: time.Now(),
		Source:      source,
		SourceURL:   sourceURL,
	}
	if err := m.reg.Upsert(rec); err != nil {
		return nil, err
	}
	m.cache[mf.ID] = mf
	info := m.toInfo(mf, rec)
	return &info, nil
}

// UpdateWebMode 更新 web 插件模式（live/record/offline）并落盘 package.json。
func (m *Manager) UpdateWebMode(id, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mf, ok := m.cache[id]
	if !ok || mf == nil {
		return fmt.Errorf("插件 %s 不可用", id)
	}
	switch mode {
	case "live", "record", "offline":
	default:
		return fmt.Errorf("未知模式 %q（live/record/offline）", mode)
	}
	mf.Mode = mode
	pkgPath := filepath.Join(m.pluginDir(id), "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	wt, _ := doc["webtool"].(map[string]any)
	if wt == nil {
		wt = map[string]any{}
		doc["webtool"] = wt
	}
	wt["mode"] = mode
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pkgPath, b, 0o644)
}

var iconHTTPClient = &http.Client{Timeout: 15 * time.Second}

// iconExtByContentType 由 Content-Type 推图标扩展名。
func iconExtByContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch {
	case strings.Contains(ct, "svg"):
		return ".svg"
	case strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "gif"):
		return ".gif"
	case strings.Contains(ct, "x-icon") || strings.Contains(ct, "vnd.microsoft.icon"):
		return ".ico"
	default:
		return ".png"
	}
}

// localizeIcon 把 http(s) 图标下载到插件目录内并把清单改写为相对路径。
// 失败静默保留原 URL（前端仍有网络加载兜底）。
func (m *Manager) localizeIcon(mf *manifest.Manifest) {
	if !strings.HasPrefix(mf.Icon, "http://") && !strings.HasPrefix(mf.Icon, "https://") {
		return
	}
	resp, err := iconHTTPClient.Get(mf.Icon) //nolint:noctx // 一次性下载，自带超时
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || len(data) == 0 {
		return
	}
	rel := "icon" + iconExtByContentType(resp.Header.Get("Content-Type"))
	if err := os.WriteFile(filepath.Join(mf.Dir, rel), data, 0o644); err != nil {
		return
	}
	old := mf.Icon
	mf.Icon = rel
	if err := m.persistManifestField(mf, "icon", rel); err != nil {
		mf.Icon = old // 落盘失败则回滚内存值，保持一致
	}
}

// persistManifestField 更新 package.json 中 unihub.<field> 并落盘。
func (m *Manager) persistManifestField(mf *manifest.Manifest, field, value string) error {
	pkgPath := filepath.Join(m.pluginDir(mf.ID), "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	u, _ := doc["unihub"].(map[string]any)
	if u == nil {
		u = map[string]any{}
		doc["unihub"] = u
	}
	u[field] = value
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pkgPath, append(b, '\n'), 0o644)
}

// LocalizeIcon 对已安装插件执行图标本地化（幂等），返回新的相对路径；
// 无需本地化（已是本地图标）返回空串。
func (m *Manager) LocalizeIcon(id string) (string, error) {
	mf, err := m.Manifest(id)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(mf.Icon, "http://") && !strings.HasPrefix(mf.Icon, "https://") {
		return "", nil
	}
	m.localizeIcon(mf)
	return mf.Icon, nil
}

func (m *Manager) Uninstall(id string, deleteData bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.reg.Get(id); !ok {
		return fmt.Errorf("插件 %s 未安装", id)
	}
	if err := os.RemoveAll(m.pluginDir(id)); err != nil {
		return err
	}
	delete(m.cache, id)
	if err := m.reg.Remove(id); err != nil {
		return err
	}
	if deleteData {
		// plugin-data/<id>.json 由 pluginapi 管理，此处按约定路径删除
		if dataDir := filepath.Dir(m.pluginsDir); dataDir != "" {
			_ = os.Remove(filepath.Join(dataDir, "plugin-data", id+".json"))
		}
	}
	return nil
}

func (m *Manager) SetEnabled(id string, enabled bool) error {
	rec, ok := m.reg.Get(id)
	if !ok {
		return fmt.Errorf("插件 %s 未安装", id)
	}
	rec.Enabled = enabled
	return m.reg.Upsert(rec)
}

// Manifest 读取（并缓存）插件清单。
func (m *Manager) Manifest(id string) (*manifest.Manifest, error) {
	m.mu.RLock()
	if mf, ok := m.cache[id]; ok {
		m.mu.RUnlock()
		if mf == nil {
			return nil, fmt.Errorf("插件 %s 清单不可用", id)
		}
		return mf, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if mf, ok := m.cache[id]; ok { // 双检
		if mf == nil {
			return nil, fmt.Errorf("插件 %s 清单不可用", id)
		}
		return mf, nil
	}
	dir := m.pluginDir(id)
	pkgPath := filepath.Join(dir, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		if os.IsNotExist(err) {
			raw = nil
		} else {
			m.cache[id] = nil
			return nil, err
		}
	}
	fallbackName := id
	mf, perr := manifest.Parse(raw, id, fallbackName)
	if perr != nil {
		m.cache[id] = nil
		return nil, perr
	}
	mf.Dir = dir
	manifest.InferEntry(dir, mf)
	m.cache[id] = mf
	return mf, nil
}

func (m *Manager) List() []Info {
	out := []Info{}
	for _, rec := range m.reg.List() {
		mf, err := m.Manifest(rec.ID)
		if err != nil || mf == nil {
			continue
		}
		out = append(out, m.toInfo(mf, rec))
	}
	// 按名称排序，输出稳定
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (m *Manager) Get(id string) (*Info, error) {
	rec, ok := m.reg.Get(id)
	if !ok {
		return nil, fmt.Errorf("插件 %s 未安装", id)
	}
	mf, err := m.Manifest(id)
	if err != nil {
		return nil, err
	}
	info := m.toInfo(mf, rec)
	return &info, nil
}

func (m *Manager) toInfo(mf *manifest.Manifest, rec registry.Record) Info {
	return Info{
		ID:              mf.ID,
		Name:            mf.Name,
		Version:         mf.Version,
		Description:     mf.Description,
		Author:          mf.Author,
		Icon:            mf.Icon,
		Category:        mf.Category,
		Keywords:        mf.Keywords,
		Permissions:     mf.Permissions,
		Homepage:        mf.Homepage,
		Type:            mf.Type,
		SourceURL:       mf.SourceURL,
		Mode:            mf.Mode,
		Sandbox:         mf.Sandbox,
		Enabled:         rec.Enabled,
		InstalledAt:     rec.InstalledAt,
		Source:          rec.Source,
		Entry:           mf.Entry,
		HasInterceptors: len(mf.Interceptors) > 0,
	}
}

// HasPermission 判断插件是否声明了某权限。
func (m *Manager) HasPermission(id, perm string) bool {
	mf, err := m.Manifest(id)
	if err != nil || mf == nil {
		return false
	}
	for _, p := range mf.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// ---- zip 工具函数 ----

// readPackageJSON 读取 zip 内 package.json（根目录或单层顶层目录）。
func readPackageJSON(zr *zip.Reader) ([]byte, error) {
	root := locatePackageRoot(zr)
	name := "package.json"
	if root != "" {
		name = root + "/package.json"
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 1<<20))
		}
	}
	if root != "" {
		// 双保险：也看一眼根目录
		for _, f := range zr.File {
			if f.Name == "package.json" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 1<<20))
			}
		}
	}
	return nil, os.ErrNotExist
}

// locatePackageRoot 若 zip 所有条目都位于同一个顶层目录且其中含
// package.json / index.html，返回该目录名；否则返回空串（根即插件根）。
func locatePackageRoot(zr *zip.Reader) string {
	top := ""
	for _, f := range zr.File {
		name := strings.TrimPrefix(filepath.ToSlash(f.Name), "./")
		if name == "" || strings.HasPrefix(name, "__MACOSX") {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 1 {
			return "" // 根下有文件 → 根即根
		}
		if top == "" {
			top = parts[0]
		} else if top != parts[0] {
			return ""
		}
	}
	if top == "" {
		return ""
	}
	// 顶层目录里要有 package.json 或 index.html 才算清单根
	want := top + "/package.json"
	index := top + "/index.html"
	hasPkg, hasIndex := false, false
	for _, f := range zr.File {
		if f.Name == want {
			hasPkg = true
		}
		if f.Name == index {
			hasIndex = true
		}
	}
	if hasPkg || hasIndex {
		return top
	}
	return ""
}

// extractZip 安全解压：拒绝绝对路径与 ".." 穿越，保留可执行位。
func extractZip(zr *zip.Reader, dest string) error {
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
			return fmt.Errorf("zip 条目路径不安全: %q", f.Name)
		}
		clean := filepath.Clean(name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("zip 条目路径不安全: %q", f.Name)
		}
		target := filepath.Join(dest, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
		// 保留可执行位（sidecar 二进制需要）
		if f.FileInfo().Mode()&0o111 != 0 {
			_ = os.Chmod(target, 0o755)
		}
	}
	return nil
}
