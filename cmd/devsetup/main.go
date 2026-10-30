// devsetup：开发辅助命令。
// 用法：
//
//	go run -tags gtk3 ./cmd/devsetup <zip路径>...   安装 zip（默认 ./plugin/*.zip）
//	go run -tags gtk3 ./cmd/devsetup -fixicons     把已装插件的远程图标下载到本地
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"webtool/internal/paths"
	"webtool/internal/plugin"
)

func main() {
	if err := paths.EnsureDirs(); err != nil {
		fmt.Fprintln(os.Stderr, "初始化数据目录失败:", err)
		os.Exit(1)
	}
	mgr := plugin.NewManager(paths.PluginsDir(), filepath.Join(paths.DataDir(), "plugins.json"))

	if len(os.Args) > 1 && os.Args[1] == "-fixicons" {
		fixIcons(mgr)
		return
	}

	zips := os.Args[1:]
	if len(zips) == 0 {
		matches, _ := filepath.Glob("plugin/*.zip")
		zips = matches
	}
	if len(zips) == 0 {
		fmt.Println("没有找到 zip（可传参指定路径）")
		return
	}

	failed := false
	for _, z := range zips {
		info, err := mgr.InstallZipPath(z, "file", "")
		if err != nil {
			fmt.Printf("✗ %s: %v\n", z, err)
			failed = true
			continue
		}
		fmt.Printf("✓ 已安装 %s (%s) → %s\n", info.Name, info.ID, filepath.Join(paths.PluginsDir(), info.ID))
	}
	if failed {
		os.Exit(1)
	}
}

// fixIcons 把所有已装插件的 http(s) 图标下载进插件目录并改写清单。
func fixIcons(mgr *plugin.Manager) {
	any := false
	for _, p := range mgr.List() {
		rel, err := mgr.LocalizeIcon(p.ID)
		if err != nil {
			fmt.Printf("✗ %s: %v\n", p.ID, err)
			continue
		}
		if rel != "" {
			any = true
			fmt.Printf("✓ %s: 图标已本地化 → %s\n", p.Name, rel)
		}
	}
	if !any {
		fmt.Println("没有需要本地化的远程图标")
	}
}
