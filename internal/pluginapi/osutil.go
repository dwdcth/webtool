package pluginapi

import (
	"path/filepath"

	"webtool/internal/plugin"
)

// blobDir 返回插件的 blob 存储目录（plugin-data/）。
func blobDir(mgr *plugin.Manager) string {
	return filepath.Join(filepath.Dir(mgr.PluginsDir()), "plugin-data")
}
