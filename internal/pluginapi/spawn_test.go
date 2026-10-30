package pluginapi

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webtool/internal/plugin"
)

// spawnTestStack 构造带可执行 sidecar 的插件与 API。
func spawnTestStack(t *testing.T) (*API, *plugin.Manager) {
	t.Helper()
	dir := t.TempDir()
	m := plugin.NewManager(filepath.Join(dir, "plugins"), filepath.Join(dir, "plugins.json"))
	if err := m.LoadInstalled(); err != nil {
		t.Fatal(err)
	}
	api := New(m)

	// 安装声明 spawn 权限的插件
	zipPath := filepath.Join(t.TempDir(), "sp.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range map[string]string{
		"package.json": `{"name":"sp","unihub":{"id":"sp","permissions":["spawn"]}}`,
		"index.html":   "<html></html>",
	} {
		fw, _ := w.Create(name)
		fw.Write([]byte(content))
	}
	w.Close()
	f.Close()
	if _, err := m.InstallZipPath(zipPath, "file", ""); err != nil {
		t.Fatal(err)
	}
	// 手工放置可执行 sidecar（shell 脚本回显 stdin）
	script := "#!/bin/sh\ncat; echo SIDECAR-OK\n"
	if err := os.WriteFile(filepath.Join(m.PluginsDir(), "sp", "sidecar.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return api, m
}

func TestNodeSpawn(t *testing.T) {
	api, _ := spawnTestStack(t)

	// 正常执行：stdin 透传 + stdout 返回
	res, err := api.nodeSpawn("sp", map[string]any{
		"command": "./sidecar.sh",
		"input":   "hello-stdin",
		"timeout": 10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["exitCode"] != 0 || !strings.Contains(m["stdout"].(string), "hello-stdin") || !strings.Contains(m["stdout"].(string), "SIDECAR-OK") {
		t.Fatalf("spawn 结果异常: %+v", m)
	}

	// 越权命令：绝对路径 / 上跳 / 非插件目录
	for _, bad := range []string{"/bin/sh", "../evil", "sidecar.sh", "./nope.sh"} {
		if _, err := api.nodeSpawn("sp", map[string]any{"command": bad}); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}

	// 无 spawn 权限的插件被拒
	zipPath := filepath.Join(t.TempDir(), "nosp.zip")
	f, _ := os.Create(zipPath)
	w := zip.NewWriter(f)
	fw, _ := w.Create("package.json")
	fw.Write([]byte(`{"name":"nosp","unihub":{"id":"nosp"}}`))
	fw2, _ := w.Create("index.html")
	fw2.Write([]byte("<html></html>"))
	w.Close()
	f.Close()
	api.mgr.InstallZipPath(zipPath, "file", "")
	os.WriteFile(filepath.Join(api.mgr.PluginsDir(), "nosp", "sidecar.sh"), []byte("#!/bin/sh\n"), 0o755)
	if _, err := api.nodeSpawn("nosp", map[string]any{"command": "./sidecar.sh"}); err == nil {
		t.Fatal("未声明 spawn 权限应被拒绝")
	}
}

func TestNodeSpawnTimeout(t *testing.T) {
	api, _ := spawnTestStack(t)
	// 放一个 sleep 脚本
	script := "#!/bin/sh\nsleep 5\n"
	if err := os.WriteFile(filepath.Join(api.mgr.PluginsDir(), "sp", "slow.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := api.nodeSpawn("sp", map[string]any{"command": "./slow.sh", "timeout": 3.0})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["exitCode"] != 124 {
		t.Fatalf("超时应返回 124, got %+v", res)
	}
}
