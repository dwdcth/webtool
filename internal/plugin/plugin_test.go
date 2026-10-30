package plugin

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildZip 在内存构造 zip 后落盘，files 为 name→content。
func buildZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return NewManager(filepath.Join(dir, "plugins"), filepath.Join(dir, "plugins.json"))
}

func TestInstallUnihubZip(t *testing.T) {
	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "calc.zip")
	buildZip(t, zipPath, map[string]string{
		"package.json":    `{"name":"calc","unihub":{"id":"com.test.calc","name":"计算器","entry":"dist/index.html","permissions":["clipboard"]}}`,
		"dist/index.html": "<html><body>calc</body></html>",
		"dist/app.js":     "console.log(1)",
	})
	info, err := m.InstallZipPath(zipPath, "file", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "com.test.calc" || info.Name != "计算器" || !info.Enabled {
		t.Fatalf("安装信息错误: %+v", info)
	}
	// 目录落盘正确
	if _, err := os.Stat(filepath.Join(m.PluginsDir(), "com.test.calc", "dist", "index.html")); err != nil {
		t.Fatal(err)
	}
	// 重复安装拒绝
	if _, err := m.InstallZipPath(zipPath, "file", ""); err == nil {
		t.Fatal("重复安装应被拒绝")
	}
	// 列表可见
	list := m.List()
	if len(list) != 1 || list[0].ID != "com.test.calc" {
		t.Fatalf("列表错误: %+v", list)
	}
}

func TestInstallLegacyZipNoManifest(t *testing.T) {
	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "ctool.zip")
	buildZip(t, zipPath, map[string]string{
		"index.html": "<html></html>",
		"app.js":     "x",
	})
	info, err := m.InstallZipPath(zipPath, "file", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "ctool" || info.Entry != "index.html" {
		t.Fatalf("legacy zip 处理错误: %+v", info)
	}
}

func TestInstallZipSingleTopDir(t *testing.T) {
	// zip 内有单层顶层目录（GitHub 下载常见）
	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "pkg.zip")
	buildZip(t, zipPath, map[string]string{
		"pkg/package.json":    `{"name":"nested","unihub":{"id":"nested"}}`,
		"pkg/dist/index.html": "<html></html>",
	})
	info, err := m.InstallZipPath(zipPath, "file", "")
	if err != nil {
		t.Fatal(err)
	}
	// 顶层目录应被提升为插件根
	if _, err := os.Stat(filepath.Join(m.PluginsDir(), "nested", "package.json")); err != nil {
		t.Fatalf("顶层目录未提升: %v", err)
	}
	if info.Entry != "dist/index.html" {
		t.Fatalf("entry 推断错误: %q", info.Entry)
	}
}

func TestZipPathTraversalRejected(t *testing.T) {
	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	buildZip(t, zipPath, map[string]string{
		"package.json": `{"name":"evil","unihub":{"id":"evil"}}`,
		"index.html":   "<html></html>",
		"../../../etc/pwned.txt": "boom",
	})
	if _, err := m.InstallZipPath(zipPath, "file", ""); err == nil {
		t.Fatal("路径穿越 zip 应被拒绝")
	}
	// 确认插件目录外无残留文件
	if _, err := os.Stat(filepath.Join(m.PluginsDir(), "evil")); err == nil {
		// 即使部分解压，危险条目也绝不能逃出临时目录
		t.Fatal("evil 目录不应存在")
	}
}

func TestUninstall(t *testing.T) {
	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "x.zip")
	buildZip(t, zipPath, map[string]string{
		"package.json": `{"name":"x","unihub":{"id":"x"}}`,
		"index.html":   "<html></html>",
	})
	if _, err := m.InstallZipPath(zipPath, "file", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall("x", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.PluginsDir(), "x")); !os.IsNotExist(err) {
		t.Fatal("插件目录未删除")
	}
	if len(m.List()) != 0 {
		t.Fatal("注册表未清空")
	}
}

func TestHasPermission(t *testing.T) {
	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "perm.zip")
	buildZip(t, zipPath, map[string]string{
		"package.json": `{"name":"perm","unihub":{"id":"perm","permissions":["clipboard","http"]}}`,
		"index.html":   "<html></html>",
	})
	if _, err := m.InstallZipPath(zipPath, "file", ""); err != nil {
		t.Fatal(err)
	}
	if !m.HasPermission("perm", "clipboard") || !m.HasPermission("perm", "http") {
		t.Fatal("声明权限应通过")
	}
	if m.HasPermission("perm", "fs") || m.HasPermission("perm", "spawn") {
		t.Fatal("未声明权限应拒绝")
	}
}

func TestLocalizeIcon(t *testing.T) {
	// 远程图标服务（模拟 iconify）
	iconData := []byte{0x89, 0x50, 0x4e, 0x47} // PNG 魔数
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte("<svg>ok</svg>"))
	}))
	defer srv.Close()

	m := newTestManager(t)
	zipPath := filepath.Join(t.TempDir(), "ico.zip")
	buildZip(t, zipPath, map[string]string{
		"package.json": `{"name":"ico","unihub":{"id":"ico","icon":"` + srv.URL + `/icon.svg"}}`,
		"index.html":   "<html></html>",
	})
	info, err := m.InstallZipPath(zipPath, "file", "")
	if err != nil {
		t.Fatal(err)
	}
	// 安装时应已自动本地化
	if info.Icon == srv.URL || info.Icon == "" {
		t.Fatalf("图标未本地化: %q", info.Icon)
	}
	if info.Icon != "icon.svg" {
		t.Fatalf("扩展名应按 Content-Type 推断为 .svg: %q", info.Icon)
	}
	// 文件落盘 + package.json 改写
	if _, err := os.Stat(filepath.Join(m.PluginsDir(), "ico", "icon.svg")); err != nil {
		t.Fatal(err)
	}
	pkg, _ := os.ReadFile(filepath.Join(m.PluginsDir(), "ico", "package.json"))
	if !strings.Contains(string(pkg), `"icon": "icon.svg"`) {
		t.Fatalf("package.json 未改写: %s", string(pkg))
	}
	// 幂等：已是本地图标再调返回空
	if rel, err := m.LocalizeIcon("ico"); err != nil || rel != "" {
		t.Fatalf("LocalizeIcon 幂等失败: %q %v", rel, err)
	}
	_ = iconData
}

func TestLocatePackageRoot(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z.zip")
	buildZip(t, p, map[string]string{"top/index.html": "x"})
	f, _ := os.Open(p)
	defer f.Close()
	st, _ := f.Stat()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if root := locatePackageRoot(zr); root != "top" {
		t.Fatalf("locatePackageRoot = %q, want top", root)
	}
	if !strings.HasPrefix("check", "ch") {
		t.Fail()
	}
}
