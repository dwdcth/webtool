// Package paths 统一管理应用数据目录布局。
package paths

import (
	"os"
	"path/filepath"
)

// DataDir 返回应用数据根目录（Linux: ~/.config/webtool）。
// 可用 WEBTOOL_DATA_DIR 环境变量覆盖（调试隔离/便携部署）。
func DataDir() string {
	if dir := os.Getenv("WEBTOOL_DATA_DIR"); dir != "" {
		return dir
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "webtool")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".webtool")
}

func PluginsDir() string    { return filepath.Join(DataDir(), "plugins") }
func PluginDataDir() string { return filepath.Join(DataDir(), "plugin-data") }
func DownloadsDir() string  { return filepath.Join(DataDir(), "downloads") }

// EnsureDirs 创建全部数据子目录。
func EnsureDirs() error {
	for _, d := range []string{DataDir(), PluginsDir(), PluginDataDir(), DownloadsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
